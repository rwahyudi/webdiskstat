package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	compactDiffBinaryMetadataLimit = 16 << 20
	compactDiffBinaryMagic         = "GDD\x02"
)

// WriteBinary serializes the compact report as GDD v2. The gzip container and
// HTML envelope are supplied by the report renderer. The stream is:
// magic, metadata JSON length+bytes, string-table count and length-prefixed
// UTF-8 strings, preorder node count and 8-uvarint base records, then overlay
// count and 7-uvarint sparse before/status records. String references are
// one-based (zero means absent); overlay indices are delta-coded.
func (r *compactDiffReport) WriteBinary(w io.Writer) error {
	return writeCompactDiffBinary(w, r)
}

type compactDiffBinaryStrings struct {
	values []string
	index  map[string]uint64
}

func (s *compactDiffBinaryStrings) intern(value string) uint64 {
	if value == "" {
		return 0
	}
	if ref, ok := s.index[value]; ok {
		return ref
	}
	ref := uint64(len(s.values) + 1)
	s.index[value] = ref
	s.values = append(s.values, value)
	return ref
}

type compactDiffBinaryFrame struct {
	id, next int32
	depth    int
	parent   string
}

func writeCompactDiffBinary(w io.Writer, r *compactDiffReport) error {
	if r == nil || r.root < 0 || r.root >= len(r.rows) {
		return errors.New("compact diff contains an invalid root")
	}
	if r.Count < 1 || r.Count > maxReportNodes || len(r.rows) > maxReportNodes {
		return fmt.Errorf("compact diff node count exceeds supported limit (%d)", maxReportNodes)
	}
	metadata := struct {
		Before       DiffSnapshot   `json:"before"`
		After        DiffSnapshot   `json:"after"`
		Complete     bool           `json:"complete"`
		Increased    int64          `json:"increased"`
		Decreased    int64          `json:"decreased"`
		StatusCounts map[string]int `json:"statusCounts"`
		Rankings     DiffRankings   `json:"rankings"`
	}{r.Before, r.After, r.Complete, r.Increased, r.Decreased, r.StatusCounts, r.Rankings}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(metadataBytes) > compactDiffBinaryMetadataLimit {
		return fmt.Errorf("compact diff metadata exceeds %d bytes", compactDiffBinaryMetadataLimit)
	}

	strings := compactDiffBinaryStrings{index: make(map[string]uint64)}
	order := make([]int32, 0, r.Count)
	stack := []compactDiffBinaryFrame{{id: int32(r.root), next: -2}}
	for len(stack) > 0 {
		frame := &stack[len(stack)-1]
		if frame.next == -2 {
			if frame.depth > compactMaxDepth {
				return errors.New("compact diff output exceeds maximum depth")
			}
			if int(frame.id) < 0 || int(frame.id) >= len(r.rows) {
				return errors.New("compact diff contains an invalid child reference")
			}
			row := r.rows[frame.id]
			if row.before < 0 || row.after < 0 {
				return errors.New("compact diff contains a negative size")
			}
			if row.beforeType > compactTypeDir || row.afterType > compactTypeDir {
				return errors.New("compact diff contains an invalid node type")
			}
			if len(order) >= maxReportNodes {
				return fmt.Errorf("compact diff node count exceeds supported limit (%d)", maxReportNodes)
			}
			order = append(order, frame.id)
			strings.intern(row.name)
			if makePath(frame.parent, row.name) != row.fullPath {
				strings.intern(row.fullPath)
			}
			strings.intern(row.afterMTime)
			frame.next = row.firstChild
		}
		for frame.next >= 0 {
			if int(frame.next) >= len(r.rows) {
				return errors.New("compact diff contains an invalid child reference")
			}
			if emitCompactDiffRow(r, frame.next) {
				break
			}
			frame.next = r.rows[frame.next].nextSibling
		}
		if frame.next >= 0 {
			child := frame.next
			if int(child) >= len(r.rows) {
				return errors.New("compact diff contains an invalid child reference")
			}
			frame.next = r.rows[child].nextSibling
			stack = append(stack, compactDiffBinaryFrame{id: child, next: -2, depth: frame.depth + 1, parent: r.rows[frame.id].fullPath})
			continue
		}
		stack = stack[:len(stack)-1]
	}
	if len(order) != r.Count {
		return fmt.Errorf("compact diff emitted %d nodes, report count is %d", len(order), r.Count)
	}
	var overlayCount uint64
	for _, id := range order {
		row := r.rows[id]
		if compactDiffNeedsOverlay(row) {
			overlayCount++
			strings.intern(row.beforeMTime)
		}
	}
	if uint64(len(strings.values)) > uint64(maxReportNodes)*4 {
		return fmt.Errorf("compact diff string table exceeds supported limit")
	}

	bw := bufio.NewWriterSize(w, compactDiffChunkSize)
	if err := writeAll(bw, []byte(compactDiffBinaryMagic)); err != nil {
		return err
	}
	if err := writeCompactDiffUvarint(bw, uint64(len(metadataBytes))); err != nil {
		return err
	}
	if err := writeAll(bw, metadataBytes); err != nil {
		return err
	}
	if err := writeCompactDiffUvarint(bw, uint64(len(strings.values))); err != nil {
		return err
	}
	for _, value := range strings.values {
		if err := writeCompactDiffUvarint(bw, uint64(len(value))); err != nil {
			return err
		}
		if err := writeAll(bw, []byte(value)); err != nil {
			return err
		}
	}
	if err := writeCompactDiffUvarint(bw, uint64(len(order))); err != nil {
		return err
	}
	baseStack := []compactDiffBinaryFrame{{id: int32(r.root), next: -2}}
	for len(baseStack) > 0 {
		frame := &baseStack[len(baseStack)-1]
		if frame.next == -2 {
			row := r.rows[frame.id]
			pathOverride := uint64(0)
			if makePath(frame.parent, row.name) != row.fullPath {
				pathOverride = strings.index[row.fullPath]
			}
			if err := writeCompactDiffBaseRecord(bw, r, row, strings.index, pathOverride); err != nil {
				return err
			}
			frame.next = row.firstChild
		}
		for frame.next >= 0 {
			if int(frame.next) >= len(r.rows) {
				return errors.New("compact diff contains an invalid child reference")
			}
			if emitCompactDiffRow(r, frame.next) {
				break
			}
			frame.next = r.rows[frame.next].nextSibling
		}
		if frame.next >= 0 {
			child := frame.next
			frame.next = r.rows[child].nextSibling
			baseStack = append(baseStack, compactDiffBinaryFrame{id: child, next: -2, parent: r.rows[frame.id].fullPath})
			continue
		}
		baseStack = baseStack[:len(baseStack)-1]
	}
	if err := writeCompactDiffUvarint(bw, overlayCount); err != nil {
		return err
	}
	var previous uint64
	firstOverlay := true
	for i, id := range order {
		row := r.rows[id]
		if !compactDiffNeedsOverlay(row) {
			continue
		}
		index := uint64(i)
		delta := index
		if !firstOverlay {
			delta = index - previous
		}
		if err := writeCompactDiffUvarint(bw, delta); err != nil {
			return err
		}
		previous = index
		firstOverlay = false
		for _, value := range [...]uint64{uint64(row.beforeType), uint64(row.before), uint64(row.beforeFiles), uint64(row.beforeItems), strings.index[row.beforeMTime], uint64(row.status)} {
			if err := writeCompactDiffUvarint(bw, value); err != nil {
				return err
			}
		}
	}
	return bw.Flush()
}

func emitCompactDiffRow(r *compactDiffReport, id int32) bool {
	return r.Complete || r.rows[id].keep
}

func compactDiffNeedsOverlay(row compactDiffRow) bool {
	return row.beforeType != row.afterType || row.before != row.after || row.beforeFiles != row.afterFiles || row.beforeItems != row.afterItems || row.beforeMTime != row.afterMTime || row.status != compactStatusUnchanged
}

func writeCompactDiffBaseRecord(w io.Writer, r *compactDiffReport, row compactDiffRow, refs map[string]uint64, pathOverride uint64) error {
	afterType := row.afterType
	afterSize, afterFiles, afterItems, afterMTime := row.after, row.afterFiles, row.afterItems, row.afterMTime
	if afterType == compactTypeUnknown {
		afterSize, afterFiles, afterItems, afterMTime = 0, 0, 0, ""
	}
	for _, value := range [...]uint64{refs[row.name], pathOverride, uint64(afterType), uint64(afterSize), uint64(afterFiles), uint64(afterItems), refs[afterMTime], uint64(compactDiffEmittedChildren(r, row))} {
		if err := writeCompactDiffUvarint(w, value); err != nil {
			return err
		}
	}
	return nil
}

func compactDiffEmittedChildren(r *compactDiffReport, row compactDiffRow) uint64 {
	var count uint64
	for child := row.firstChild; child >= 0; child = r.rows[child].nextSibling {
		if emitCompactDiffRow(r, child) {
			count++
		}
	}
	return count
}

func writeCompactDiffUvarint(w io.Writer, value uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], value)
	return writeAll(w, buf[:n])
}
