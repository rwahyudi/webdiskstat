package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// compactDiffReport deliberately stores only the compact comparison data. A
// DiffNode is assembled one-at-a-time by WriteJSON; the report never owns a
// pointer tree proportional to the number of input nodes.
type compactDiffReport struct {
	Before, After        DiffSnapshot
	Complete             bool
	Increased, Decreased int64
	StatusCounts         map[string]int
	Rankings             DiffRankings
	Count                int
	rows                 []compactDiffRow
	root                 int
}

type compactDiffRow struct {
	name, fullPath, beforeMTime, afterMTime          string
	before, after                                    int64
	beforeFiles, afterFiles, beforeItems, afterItems uint32
	firstChild, nextSibling                          int32
	beforeType, afterType, typ                       compactNodeType
	status                                           compactDiffStatus
	keep, metadataChanged                            bool
}

type compactSnapshotRow struct {
	name, fullPath, mtime, flag, mime string
	typ                               compactNodeType
	size                              int64
	files, items                      uint32
	firstChild, nextSibling           int32
}

type compactNodeType uint8

const (
	compactTypeUnknown compactNodeType = iota
	compactTypeFile
	compactTypeDir
)

func nodeTypeCode(value string) compactNodeType {
	if value == "dir" {
		return compactTypeDir
	}
	if value == "file" {
		return compactTypeFile
	}
	return compactTypeUnknown
}
func (t compactNodeType) String() string {
	switch t {
	case compactTypeFile:
		return "file"
	case compactTypeDir:
		return "dir"
	}
	return ""
}

type compactDiffStatus uint8

const (
	compactStatusUnchanged compactDiffStatus = iota
	compactStatusAdded
	compactStatusRemoved
	compactStatusGrown
	compactStatusShrunk
	compactStatusChanged
)

func (s compactDiffStatus) String() string {
	switch s {
	case compactStatusAdded:
		return "added"
	case compactStatusRemoved:
		return "removed"
	case compactStatusGrown:
		return "grown"
	case compactStatusShrunk:
		return "shrunk"
	case compactStatusChanged:
		return "changed"
	}
	return "unchanged"
}

type compactSnapshot struct {
	rows      []compactSnapshotRow
	root      int
	meta      diffMetadata
	pathBytes int64
}

const compactMaxDepth = 4096
const compactCompatLimit = 16 << 20
const compactTokenStringLimit = 1 << 20
const compactMetadataBudget = 1 << 20
const compactCompatPathBudget = 64 << 20
const compactNativePathBudget = 512 << 20

// buildCompactDiffReport is the streaming entry point used by the CLI. Native
// GDU sequence exports are decoded directly into flat rows rather than first
// constructing the legacy *Node graph.
func buildCompactDiffReport(beforeSource, afterSource string, stdin io.Reader, complete bool) (*compactDiffReport, error) {
	if beforeSource == "-" && afterSource == "-" {
		return nil, errors.New("stdin can only be used for one snapshot")
	}
	b, err := readCompactSnapshot(beforeSource, stdin, "baseline")
	if err != nil {
		return nil, err
	}
	a, err := readCompactSnapshot(afterSource, stdin, "current")
	if err != nil {
		return nil, err
	}
	return compareCompactSnapshots(b, a, beforeSource, afterSource, complete)
}

func readCompactSnapshot(source string, stdin io.Reader, label string) (*compactSnapshot, error) {
	var input io.Reader
	var closeFn func() error = func() error { return nil }
	if source == "-" {
		input = stdin
	} else {
		f, err := openCompactInput(source)
		if err != nil {
			return nil, err
		}
		input, closeFn = f, f.Close
	}
	defer closeFn()
	br := bufio.NewReader(newSizeLimitedReader(input, maxInputBytes))
	gz := filepath.Ext(source) == ".gz"
	if h, err := br.Peek(2); err == nil && h[0] == 0x1f && h[1] == 0x8b {
		gz = true
	}
	rowCapacityHint := compactRowCapacityHint(input, gz)
	var r io.Reader = br
	var gzReader *gzip.Reader
	if gz {
		var err error
		gzReader, err = gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gzReader.Close()
		r = gzReader
	}
	captured := &compactCaptureReader{r: newSizeLimitedReader(r, maxDecompressedBytes)}
	guard := &compactJSONGuard{r: captured, stringLimit: compactTokenStringLimit, numericLimit: compactTokenStringLimit, depthLimit: compactMaxDepth}
	d := json.NewDecoder(guard)
	d.UseNumber()
	snap, err := parseCompactExport(d, label, rowCapacityHint)
	if err != nil {
		var hard *compactHardError
		if errors.As(err, &hard) {
			return nil, err
		}
		if _, drainErr := io.Copy(io.Discard, captured); drainErr != nil {
			return nil, drainErr
		}
		if guard.failed != nil {
			return nil, guard.failed
		}
		if captured.tooLarge {
			return nil, fmt.Errorf("%s snapshot uses an unsupported format larger than the %d-byte compact compatibility limit", label, compactCompatLimit)
		}
		var raw any
		dec := json.NewDecoder(bytes.NewReader(captured.buf))
		dec.UseNumber()
		if decodeErr := dec.Decode(&raw); decodeErr != nil {
			return nil, err
		}
		if _, decodeErr := dec.Token(); decodeErr != io.EOF {
			return nil, err
		}
		if preflightErr := compactCompatibilityPreflight(raw, label); preflightErr != nil {
			return nil, preflightErr
		}
		root, meta, normalizeErr := normalizeGDUForDiff(raw)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		return compactSnapshotFromNode(root, meta, label)
	}
	if _, err = d.Token(); err != io.EOF {
		if guard.failed != nil {
			return nil, guard.failed
		}
		if err == nil {
			return nil, errors.New("input contains multiple JSON values")
		}
		return nil, err
	}
	return snap, nil
}

type compactHardError struct{ message string }

func (e *compactHardError) Error() string { return e.message }
func compactHardErrorf(format string, args ...any) error {
	return &compactHardError{message: fmt.Sprintf(format, args...)}
}

func compactRowCapacityHint(input io.Reader, gz bool) int {
	f, ok := input.(*os.File)
	if !ok {
		return 0
	}
	info, err := f.Stat()
	if err != nil {
		return 0
	}
	bytes := info.Size()
	if gz && bytes >= 4 {
		var footer [4]byte
		if _, err = f.ReadAt(footer[:], bytes-4); err == nil {
			expanded := int64(binary.LittleEndian.Uint32(footer[:]))
			if expanded > 0 && expanded <= maxDecompressedBytes {
				bytes = expanded
			}
		}
	}
	// Native GDU entries average roughly this many JSON bytes; a conservative
	// estimate avoids repeated large backing-array copies without excessive slack.
	capacity := bytes / 72
	if capacity < 1024 {
		capacity = 1024
	}
	if capacity > 3_000_000 {
		capacity = 3_000_000
	}
	return int(capacity)
}

func compactCompatibilityPreflight(raw any, label string) error {
	tree := raw
	metaRoot := ""
	if arr, ok := raw.([]any); ok && isExportArray(arr) {
		if m, ok := arr[2].(map[string]any); ok {
			metaRoot = metadataValue(m, "root", "Root", "path", "Path", "root_path", "rootPath")
		}
		tree = arr[3]
	} else if m, ok := raw.(map[string]any); ok {
		metaRoot = metadataValue(wrappedGDUResourceMetadata(m), "root", "Root", "path", "Path", "root_path", "rootPath")
		tree = unwrapPossibleRoot(m)
	}
	type frame struct {
		value     any
		parentLen int64
		fallback  string
		depth     int
		root      bool
	}
	stack := []frame{{value: tree, fallback: "root", root: true}}
	var nodes, pathCost int64
	add := func(pathLen int64) error {
		nodes++
		pathCost += pathLen + 32
		if nodes > maxReportNodes {
			return fmt.Errorf("%s snapshot contains more than %d nodes", label, maxReportNodes)
		}
		if pathCost > compactCompatPathBudget {
			return fmt.Errorf("%s snapshot exceeds compact compatibility path budget (%d bytes)", label, compactCompatPathBudget)
		}
		return nil
	}
	makeLen := func(parent int64, name string) int64 {
		if parent == 0 {
			return int64(len(name))
		}
		return parent + 1 + int64(len(name))
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.depth > compactMaxDepth {
			return fmt.Errorf("%s snapshot exceeds compact parser depth limit (%d)", label, compactMaxDepth)
		}
		switch value := f.value.(type) {
		case []any:
			if isSequenceNode(value) {
				info, _ := value[0].(map[string]any)
				name := firstString(info, nameKeys)
				p := firstString(info, pathKeys)
				if p != "" && (name == "" || name == p) {
					name = displayNameFromPath(p)
				}
				if name == "" {
					name = f.fallback
				}
				plen := makeLen(f.parentLen, name)
				if p != "" {
					plen = int64(len(p))
					if f.parentLen > 0 && p == name {
						plen = makeLen(f.parentLen, name)
					}
				}
				if f.root && name == "root" && (p == "" || p == "root") && metaRoot != "" {
					plen = int64(len(metaRoot))
				}
				if err := add(plen); err != nil {
					return err
				}
				for i := len(value) - 1; i >= 1; i-- {
					stack = append(stack, frame{value: value[i], parentLen: plen, fallback: fmt.Sprintf("item-%d", i-1), depth: f.depth + 1})
				}
			} else {
				plen := makeLen(f.parentLen, f.fallback)
				if err := add(plen); err != nil {
					return err
				}
				if f.root {
					for i := len(value) - 1; i >= 0; i-- {
						stack = append(stack, frame{value: value[i], fallback: fmt.Sprintf("item-%d", i), depth: f.depth + 1})
					}
				} else {
					for i := len(value) - 1; i >= 0; i-- {
						stack = append(stack, frame{value: value[i], parentLen: plen, fallback: f.fallback, depth: f.depth + 1})
					}
				}
			}
		case map[string]any:
			name := firstString(value, nameKeys)
			p := firstString(value, pathKeys)
			if p != "" && (name == "" || name == p) {
				name = displayNameFromPath(p)
			}
			if name == "" {
				name = f.fallback
			}
			plen := makeLen(f.parentLen, name)
			if p != "" {
				plen = int64(len(p))
				if f.parentLen > 0 && p == name {
					plen = makeLen(f.parentLen, name)
				}
			}
			if f.root && name == "root" && (p == "" || p == "root") && metaRoot != "" {
				plen = int64(len(metaRoot))
			}
			if err := add(plen); err != nil {
				return err
			}
			children, has := extractChildren(value)
			if has {
				switch ch := children.(type) {
				case []any:
					for i := len(ch) - 1; i >= 0; i-- {
						stack = append(stack, frame{value: ch[i], parentLen: plen, fallback: fmt.Sprintf("item-%d", i), depth: f.depth + 1})
					}
				case map[string]any:
					for k, v := range ch {
						stack = append(stack, frame{value: v, parentLen: plen, fallback: k, depth: f.depth + 1})
					}
				}
			} else if !looksLikeNode(value) {
				for k, v := range value {
					switch v.(type) {
					case map[string]any, []any, json.Number, float64, int, int64:
						stack = append(stack, frame{value: v, parentLen: plen, fallback: k, depth: f.depth + 1})
					}
				}
			}
		default:
			plen := makeLen(f.parentLen, f.fallback)
			if err := add(plen); err != nil {
				return err
			}
		}
	}
	return nil
}

type compactCaptureReader struct {
	r        io.Reader
	buf      []byte
	tooLarge bool
}

type compactJSONGuard struct {
	r                                     io.Reader
	stringLimit, numericLimit, depthLimit int
	depth, stringLength, numericLength    int
	inString, escaped                     bool
	failed                                error
}

func (g *compactJSONGuard) Read(p []byte) (int, error) {
	if g.failed != nil {
		return 0, g.failed
	}
	n, err := g.r.Read(p)
	for i := 0; i < n; i++ {
		b := p[i]
		if g.inString {
			if g.escaped {
				g.escaped = false
				g.stringLength++
			} else if b == '\\' {
				g.escaped = true
				g.stringLength++
			} else if b == '"' {
				g.inString = false
				g.stringLength = 0
			} else {
				g.stringLength++
			}
			if g.inString && g.stringLength > g.stringLimit {
				g.failed = fmt.Errorf("JSON string token exceeds compact limit (%d bytes)", g.stringLimit)
				return i, g.failed
			}
			continue
		}
		if (b >= '0' && b <= '9') || b == '-' || b == '+' || b == '.' || b == 'e' || b == 'E' {
			g.numericLength++
			if g.numericLength > g.numericLimit {
				g.failed = fmt.Errorf("JSON numeric token exceeds compact limit (%d bytes)", g.numericLimit)
				return i, g.failed
			}
		} else {
			g.numericLength = 0
		}
		switch b {
		case '"':
			g.inString = true
			g.stringLength = 0
		case '{', '[':
			g.depth++
			if g.depth > g.depthLimit {
				g.failed = fmt.Errorf("JSON nesting exceeds compact limit (%d)", g.depthLimit)
				return i, g.failed
			}
		case '}', ']':
			g.depth--
		}
	}
	return n, err
}

func (c *compactCaptureReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && !c.tooLarge {
		if len(c.buf)+n > compactCompatLimit {
			c.tooLarge = true
			c.buf = nil
		} else {
			c.buf = append(c.buf, p[:n]...)
		}
	}
	return n, err
}

func compactSnapshotFromNode(root *Node, meta diffMetadata, label string) (*compactSnapshot, error) {
	if root == nil {
		return nil, fmt.Errorf("%s snapshot has no root", label)
	}
	s := &compactSnapshot{root: -1, meta: meta}
	type frame struct {
		n             *Node
		parent, depth int
	}
	stack := []frame{{n: root, parent: -1, depth: 0}}
	lastChild := make([]int32, 0, 1024)
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.depth > compactMaxDepth {
			return nil, fmt.Errorf("%s snapshot exceeds compact parser depth limit", label)
		}
		if len(s.rows) >= maxReportNodes {
			return nil, fmt.Errorf("%s snapshot contains more than %d nodes", label, maxReportNodes)
		}
		id := len(s.rows)
		r := compactSnapshotRow{name: f.n.Name, fullPath: f.n.Path, mtime: f.n.MTime, flag: f.n.Flag, mime: f.n.MIME, typ: nodeTypeCode(f.n.Type), size: f.n.Size, files: uint32(f.n.Files), items: uint32(f.n.Items), firstChild: -1, nextSibling: -1}
		s.pathBytes += int64(len(r.fullPath))
		if r.name != path.Base(r.fullPath) {
			s.pathBytes += int64(len(r.name))
		}
		if s.pathBytes > compactNativePathBudget {
			return nil, compactHardErrorf("%s snapshot exceeds compact path-storage limit (%d bytes)", label, compactNativePathBudget)
		}
		s.rows = append(s.rows, r)
		lastChild = append(lastChild, -1)
		if f.parent < 0 {
			s.root = id
		} else {
			p := f.parent
			if s.rows[p].firstChild < 0 {
				s.rows[p].firstChild = int32(id)
			} else {
				s.rows[int(lastChild[p])].nextSibling = int32(id)
			}
			lastChild[p] = int32(id)
		}
		for i := len(f.n.Children) - 1; i >= 0; i-- {
			if f.n.Children[i] == nil {
				return nil, fmt.Errorf("%s snapshot contains a null node", label)
			}
			stack = append(stack, frame{n: f.n.Children[i], parent: id, depth: f.depth + 1})
		}
	}
	return s, nil
}

func openCompactInput(source string) (io.ReadCloser, error) {
	f, err := os.Open(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%q does not exist", source)
		}
		return nil, err
	}
	return f, nil
}

// openFile is isolated to preserve the same missing-file diagnostic as readJSON.
func parseCompactExport(d *json.Decoder, label string, rowCapacityHint int) (*compactSnapshot, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('[') {
		return nil, fmt.Errorf("%s snapshot is not a native GDU sequence export; compact input requires a GDU export array", label)
	}
	version, err := d.Token()
	if err != nil {
		return nil, err
	}
	if _, ok := version.(json.Number); !ok {
		return nil, fmt.Errorf("%s snapshot is not a native GDU sequence export", label)
	}
	minor, err := d.Token()
	if err != nil {
		return nil, err
	}
	if _, ok := minor.(json.Number); !ok {
		return nil, fmt.Errorf("%s snapshot has an invalid export version", label)
	}
	meta, err := decodeCompactObject(d)
	if err != nil {
		return nil, fmt.Errorf("%s snapshot metadata: %w", label, err)
	}
	if scanner := compactScanner(meta); scanner != "" && !strings.EqualFold(scanner, "gdu") {
		return nil, scannerIdentityError(scanner)
	}
	s := &compactSnapshot{root: -1}
	s.meta = diffMetadata{root: compactScalar(meta, "root", "Root", "path", "Path", "root_path", "rootPath"), capturedAt: compactScalar(meta, "timestamp", "Timestamp", "capturedAt", "captured_at", "scan_time", "scanTime")}
	tok, err = d.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('[') {
		return nil, fmt.Errorf("%s snapshot has unsupported GDU root form", label)
	}
	idx, err := parseCompactSequenceNode(d, s, "", "root", -1, rowCapacityHint, 0, label, true)
	if err != nil {
		return nil, err
	}
	s.root = idx
	// Root node's closing array has been consumed by the node parser.
	for {
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		if t == json.Delim(']') {
			break
		}
		return nil, fmt.Errorf("%s snapshot has extra export fields", label)
	}
	if s.rows[idx].fullPath == "root" {
		if s.meta.root == "" {
			return nil, fmt.Errorf("%s snapshot root path is unavailable", label)
		}
		s.pathBytes += int64(len(s.meta.root) - len(s.rows[idx].fullPath))
		if s.pathBytes > compactNativePathBudget {
			return nil, compactHardErrorf("%s snapshot exceeds compact path-storage limit (%d bytes)", label, compactNativePathBudget)
		}
		s.rows[idx].fullPath = s.meta.root
	}
	if s.meta.root == "" {
		s.meta.root = s.rows[idx].fullPath
	}
	if s.rows[idx].fullPath == "" {
		return nil, fmt.Errorf("%s snapshot root path is unavailable", label)
	}
	if cleanDiffRoot(s.meta.root) != cleanDiffRoot(s.rows[idx].fullPath) {
		return nil, fmt.Errorf("gdu metadata root %q does not match tree root %q", s.meta.root, s.rows[idx].fullPath)
	}
	return s, nil
}

func parseCompactSequenceNode(d *json.Decoder, s *compactSnapshot, parent, fallback string, fallbackIndex, capacityHint, depth int, label string, alreadyOpen bool) (int, error) {
	if depth > compactMaxDepth {
		return -1, fmt.Errorf("%s snapshot exceeds compact parser depth limit (%d)", label, compactMaxDepth)
	}
	if !alreadyOpen {
		t, e := d.Token()
		if e != nil {
			return -1, e
		}
		if t != json.Delim('[') {
			return -1, fmt.Errorf("%s snapshot contains unsupported non-sequence child", label)
		}
	}
	info, err := decodeCompactNodeInfo(d)
	if err != nil {
		return -1, fmt.Errorf("%s snapshot node metadata: %w", label, err)
	}
	if len(s.rows) == 0 && capacityHint > 0 {
		s.rows = make([]compactSnapshotRow, 0, capacityHint)
	}
	name := info.name
	p := info.path
	if p != "" && (name == "" || name == p) {
		name = displayNameFromPath(p)
	}
	if name == "" {
		if fallback == "" {
			fallback = fmt.Sprintf("item-%d", fallbackIndex)
		}
		name = fallback
	}
	if p == "" {
		p = makePath(parent, name)
	} else if parent != "" && p == name {
		p = makePath(parent, name)
	}
	if name == path.Base(p) {
		name = path.Base(p)
	}
	s.pathBytes += int64(len(p))
	if name != path.Base(p) {
		s.pathBytes += int64(len(name))
	}
	if s.pathBytes > compactNativePathBudget {
		return -1, compactHardErrorf("%s snapshot exceeds compact path-storage limit (%d bytes)", label, compactNativePathBudget)
	}
	row := compactSnapshotRow{name: name, fullPath: p, size: info.size, mtime: info.mtime, flag: info.flag, typ: compactTypeFile, firstChild: -1, nextSibling: -1}
	idx := len(s.rows)
	if idx >= maxReportNodes {
		return -1, fmt.Errorf("%s snapshot contains more than %d nodes", label, maxReportNodes)
	}
	s.rows = append(s.rows, row)
	last := -1
	childSize := int64(0)
	childCount, files := 0, 0
	for {
		tok, e := d.Token()
		if e != nil {
			return -1, e
		}
		if tok == json.Delim(']') {
			break
		}
		var ci int
		if tok == json.Delim('[') {
			ci, e = parseCompactSequenceNode(d, s, p, "", childCount, 0, depth+1, label, true)
		} else if tok == json.Delim('{') {
			ci, e = parseCompactMappingNode(d, s, p, "", childCount, depth+1, label)
		} else {
			return -1, fmt.Errorf("%s snapshot contains unsupported GDU child form", label)
		}
		if e != nil {
			return -1, e
		}
		if last < 0 {
			s.rows[idx].firstChild = int32(ci)
		} else {
			s.rows[last].nextSibling = int32(ci)
		}
		last = ci
		childSize += s.rows[ci].size
		childCount++
		files += int(s.rows[ci].files)
	}
	isDir := true // Native sequence nodes represent directories in GDU exports.
	if isDir {
		s.rows[idx].typ = compactTypeDir
		s.rows[idx].size = info.size + childSize
	} else if s.rows[idx].size <= 0 {
		s.rows[idx].size = childSize
	}
	if !isDir {
		files++
	}
	s.rows[idx].files = uint32(files)
	s.rows[idx].items = uint32(childCount)
	for c := s.rows[idx].firstChild; c >= 0; c = s.rows[int(c)].nextSibling {
		s.rows[idx].items += s.rows[int(c)].items
	}
	if value := info.mime; value != "" && (!isDir || childCount == 0) {
		s.rows[idx].mime = strings.Split(value, ";")[0]
	} else if !isDir || childCount == 0 {
		if mt := mime.TypeByExtension(extensionFor(name)); mt != "" {
			s.rows[idx].mime = strings.Split(mt, ";")[0]
		}
	}
	return idx, nil
}

func parseCompactMappingNode(d *json.Decoder, s *compactSnapshot, parent, fallback string, fallbackIndex, depth int, label string) (int, error) {
	if depth > compactMaxDepth {
		return -1, fmt.Errorf("%s snapshot exceeds compact parser depth limit (%d)", label, compactMaxDepth)
	}
	info, err := decodeCompactNodeInfoOpen(d)
	if err != nil {
		return -1, fmt.Errorf("%s snapshot node metadata: %w", label, err)
	}
	name := info.name
	p := info.path
	if p != "" && (name == "" || name == p) {
		name = displayNameFromPath(p)
	}
	if name == "" {
		if fallback == "" {
			fallback = fmt.Sprintf("item-%d", fallbackIndex)
		}
		name = fallback
	}
	if p == "" {
		p = makePath(parent, name)
	} else if parent != "" && p == name {
		p = makePath(parent, name)
	}
	if name == path.Base(p) {
		name = path.Base(p)
	}
	s.pathBytes += int64(len(p))
	if name != path.Base(p) {
		s.pathBytes += int64(len(name))
	}
	if s.pathBytes > compactNativePathBudget {
		return -1, compactHardErrorf("%s snapshot exceeds compact path-storage limit (%d bytes)", label, compactNativePathBudget)
	}
	isDir := info.dir
	r := compactSnapshotRow{name: name, fullPath: p, size: info.size, mtime: info.mtime, flag: info.flag, typ: compactTypeFile, firstChild: -1, nextSibling: -1}
	if isDir {
		r.typ = compactTypeDir
	}
	if !isDir {
		if v := info.mime; v != "" {
			r.mime = strings.Split(v, ";")[0]
		} else if mt := mime.TypeByExtension(extensionFor(name)); mt != "" {
			r.mime = strings.Split(mt, ";")[0]
		}
	}
	if len(s.rows) >= maxReportNodes {
		return -1, fmt.Errorf("%s snapshot contains more than %d nodes", label, maxReportNodes)
	}
	id := len(s.rows)
	if !isDir {
		r.files = 1
	}
	s.rows = append(s.rows, r)
	return id, nil
}

type compactNodeInfo struct {
	name, path, mtime, flag, mime string
	size                          int64
	dir                           bool
}

func decodeCompactNodeInfo(d *json.Decoder) (compactNodeInfo, error) {
	t, err := d.Token()
	if err != nil {
		return compactNodeInfo{}, err
	}
	if t != json.Delim('{') {
		return compactNodeInfo{}, errors.New("expected metadata object")
	}
	return decodeCompactNodeInfoOpen(d)
}

func decodeCompactNodeInfoOpen(d *json.Decoder) (compactNodeInfo, error) {
	var out compactNodeInfo
	namePriority, pathPriority, mtimePriority, flagPriority, mimePriority, sizePriority, dirPriority := len(nameKeys), len(pathKeys), len(mtimeKeys), len(flagKeys), len(mimeKeys), len(sizeKeys), len(dirKeys)
	var seenRecognized [32]string
	seenCount := 0
	for d.More() {
		keyToken, err := d.Token()
		if err != nil {
			return out, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return out, errors.New("invalid object key")
		}
		if len(key) > compactTokenStringLimit {
			return out, fmt.Errorf("metadata key exceeds compact token limit (%d bytes)", compactTokenStringLimit)
		}
		if isCompactNodeField(key) {
			for i := 0; i < seenCount; i++ {
				if seenRecognized[i] == key {
					return out, fmt.Errorf("duplicate recognized metadata key %q", key)
				}
			}
			if seenCount == len(seenRecognized) {
				return out, errors.New("too many recognized metadata keys")
			}
			seenRecognized[seenCount] = key
			seenCount++
		}
		value, err := d.Token()
		if err != nil {
			return out, err
		}
		if delim, ok := value.(json.Delim); ok {
			_, skipErr := skipCompactValue(d, delim)
			if skipErr != nil {
				return out, skipErr
			}
			return out, fmt.Errorf("metadata field %q uses an unsupported composite value", key)
		}
		if text, ok := value.(string); ok && len(text) > compactTokenStringLimit {
			return out, fmt.Errorf("metadata string exceeds compact token limit (%d bytes)", compactTokenStringLimit)
		}
		if i := compactKeyPriority(nameKeys, key); i < namePriority {
			if v, ok := value.(string); ok && v != "" {
				out.name = v
				namePriority = i
			}
		}
		if i := compactKeyPriority(pathKeys, key); i < pathPriority {
			if v, ok := value.(string); ok && v != "" {
				out.path = v
				pathPriority = i
			}
		}
		if i := compactKeyPriority(sizeKeys, key); i < sizePriority {
			if n := numberish(value); n > 0 {
				out.size = n
				sizePriority = i
			}
		}
		if i := compactKeyPriority(mtimeKeys, key); i < mtimePriority {
			if v := compactScalarValue(value); v != "" || isNumericToken(value) {
				out.mtime = v
				mtimePriority = i
			}
		}
		if i := compactKeyPriority(flagKeys, key); i < flagPriority {
			if v, ok := value.(string); ok && v != "" {
				out.flag = v
				flagPriority = i
			}
		}
		if i := compactKeyPriority(mimeKeys, key); i < mimePriority {
			if v, ok := value.(string); ok && v != "" {
				out.mime = v
				mimePriority = i
			}
		}
		if i := compactKeyPriority(dirKeys, key); i < dirPriority {
			if v, ok := value.(bool); ok {
				out.dir = v
				dirPriority = i
			}
		}
	}
	_, err := d.Token()
	return out, err
}

func compactKeyPriority(keys []string, key string) int {
	for i, k := range keys {
		if k == key {
			return i
		}
	}
	return len(keys)
}
func compactScalarValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}
func isNumericToken(v any) bool { _, ok := v.(json.Number); return ok }
func isCompactNodeField(key string) bool {
	return compactKeyPriority(nameKeys, key) < len(nameKeys) || compactKeyPriority(pathKeys, key) < len(pathKeys) || compactKeyPriority(sizeKeys, key) < len(sizeKeys) || compactKeyPriority(dirKeys, key) < len(dirKeys) || compactKeyPriority(mtimeKeys, key) < len(mtimeKeys) || compactKeyPriority(flagKeys, key) < len(flagKeys) || compactKeyPriority(mimeKeys, key) < len(mimeKeys) || contains(childKeys, key)
}

func decodeCompactObject(d *json.Decoder) (map[string]any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('{') {
		return nil, fmt.Errorf("expected metadata object")
	}
	return decodeCompactObjectOpen(d)
}

func decodeCompactObjectOpen(d *json.Decoder) (map[string]any, error) {
	m := make(map[string]any, 8)
	knownBytes := 0
	for d.More() {
		kt, e := d.Token()
		if e != nil {
			return nil, e
		}
		k, ok := kt.(string)
		if !ok {
			return nil, errors.New("invalid object key")
		}
		if len(k) > compactTokenStringLimit {
			return nil, fmt.Errorf("metadata key exceeds compact token limit (%d bytes)", compactTokenStringLimit)
		}
		scalar, e := d.Token()
		if e != nil {
			return nil, e
		}
		if delim, composite := scalar.(json.Delim); composite {
			if _, e := skipCompactValue(d, delim); e != nil {
				return nil, e
			}
			if isCompactScannerKey(k) {
				return nil, fmt.Errorf("scanner metadata field %q uses an unsupported composite value", k)
			}
			continue
		}
		switch scalar.(type) {
		case string, json.Number, bool, nil:
			if text, ok := scalar.(string); ok && len(text) > compactTokenStringLimit {
				return nil, fmt.Errorf("metadata string exceeds compact token limit (%d bytes)", compactTokenStringLimit)
			}
			if isCompactMetadataKey(k) {
				knownBytes += len(k)
				switch value := scalar.(type) {
				case string:
					knownBytes += len(value)
				case json.Number:
					knownBytes += len(value.String())
				}
				if knownBytes > compactMetadataBudget {
					return nil, compactHardErrorf("snapshot export metadata exceeds compact retained-metadata limit (%d bytes)", compactMetadataBudget)
				}
				m[k] = scalar
			}
		default:
			return nil, fmt.Errorf("metadata field %q is not scalar", k)
		}
	}
	_, err := d.Token()
	return m, err
}

func isCompactScannerKey(key string) bool {
	switch strings.ToLower(key) {
	case "prog", "progname", "program", "programname":
		return true
	}
	return false
}
func isCompactMetadataKey(key string) bool {
	if isCompactScannerKey(key) {
		return true
	}
	switch key {
	case "root", "Root", "path", "Path", "root_path", "rootPath", "timestamp", "Timestamp", "capturedAt", "captured_at", "scan_time", "scanTime":
		return true
	}
	return false
}

func skipCompactValue(d *json.Decoder, opening json.Delim) (bool, error) {
	if opening != '{' && opening != '[' {
		return false, nil
	}
	hasNodeFields := false
	closing := json.Delim(']')
	if opening == '{' {
		closing = '}'
	}
	for d.More() {
		if opening == '{' {
			key, err := d.Token()
			if err != nil {
				return false, err
			}
			text, ok := key.(string)
			if ok && len(text) > compactTokenStringLimit {
				return false, fmt.Errorf("metadata key exceeds compact token limit (%d bytes)", compactTokenStringLimit)
			}
			if ok && isCompactNodeField(text) {
				hasNodeFields = true
			}
		}
		tok, err := d.Token()
		if err != nil {
			return false, err
		}
		if text, ok := tok.(string); ok && len(text) > compactTokenStringLimit {
			return false, fmt.Errorf("metadata string exceeds compact token limit (%d bytes)", compactTokenStringLimit)
		}
		if delim, ok := tok.(json.Delim); ok {
			nested, err := skipCompactValue(d, delim)
			if err != nil {
				return false, err
			}
			hasNodeFields = hasNodeFields || nested
		}
	}
	tok, err := d.Token()
	if err != nil {
		return false, err
	}
	if tok != closing {
		return false, errors.New("invalid nested JSON value")
	}
	return hasNodeFields, nil
}

func compactString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func compactScalar(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case json.Number:
			return v.String()
		}
	}
	return ""
}
func compactNumber(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if n := numberish(m[k]); n > 0 {
			return n
		}
	}
	return 0
}
func compactBool(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if b, ok := m[k].(bool); ok && b {
			return true
		}
	}
	return false
}
func compactScanner(m map[string]any) string {
	for k, v := range m {
		lk := strings.ToLower(k)
		if lk == "prog" || lk == "progname" || lk == "program" || lk == "programname" {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
			return fmt.Sprint(v)
		}
	}
	return ""
}

func compareCompactSnapshots(b, a *compactSnapshot, bs, as string, complete bool) (*compactDiffReport, error) {
	if b == nil || a == nil || b.root < 0 || a.root < 0 {
		return nil, errors.New("both snapshots must have a root")
	}
	br, ar := cleanDiffRoot(b.rows[b.root].fullPath), cleanDiffRoot(a.rows[a.root].fullPath)
	if br == "" || ar == "" {
		return nil, errors.New("snapshot root is unavailable")
	}
	if br != ar {
		return nil, fmt.Errorf("snapshot roots differ: baseline %q, current %q", br, ar)
	}
	if len(b.rows)+len(a.rows) > maxReportNodes {
		return nil, fmt.Errorf("comparison contains more than %d combined nodes", maxReportNodes)
	}
	if b.pathBytes+a.pathBytes > 2*compactNativePathBudget {
		return nil, fmt.Errorf("comparison exceeds compact combined path-storage limit (%d bytes)", 2*compactNativePathBudget)
	}
	// Build report rows in baseline order and append current-only rows directly.
	// The one canonical-path index also detects current-side duplicates and is
	// reused to connect the merged tree; no separate union-record array is kept.
	capacity := len(b.rows) + len(a.rows)/16
	if capacity > maxReportNodes {
		capacity = maxReportNodes
	}
	rows := make([]compactDiffRow, 0, capacity)
	lookup := make(map[string]int32, len(b.rows))
	for i := range b.rows {
		row := b.rows[i]
		p := cleanDiffRoot(row.fullPath)
		if p == "" {
			return nil, errors.New("baseline snapshot contains a node with unavailable path")
		}
		if _, exists := lookup[p]; exists {
			return nil, fmt.Errorf("baseline snapshot has duplicate or ambiguous path %q", p)
		}
		name := row.name
		if name == "" && p != br {
			name = path.Base(p)
		}
		rows = append(rows, compactDiffRow{name: name, fullPath: p, beforeType: row.typ, typ: row.typ, before: row.size, beforeFiles: row.files, beforeItems: row.items, beforeMTime: row.mtime, firstChild: -1, nextSibling: -1})
		lookup[p] = int32(i)
	}
	for i := range a.rows {
		row := a.rows[i]
		p := cleanDiffRoot(row.fullPath)
		if p == "" {
			return nil, errors.New("current snapshot contains a node with unavailable path")
		}
		if id, exists := lookup[p]; exists {
			joined := &rows[int(id)]
			if joined.keep {
				return nil, fmt.Errorf("current snapshot has duplicate or ambiguous path %q", p)
			}
			joined.keep = true // transient seen-after bit
			if row.name != "" {
				joined.name = row.name
			}
			joined.afterType = row.typ
			joined.after = row.size
			joined.afterFiles = row.files
			joined.afterItems = row.items
			joined.afterMTime = row.mtime
			before := b.rows[int(id)]
			joined.metadataChanged = before.mtime != row.mtime || before.flag != row.flag || before.mime != row.mime
		} else {
			name := row.name
			if name == "" {
				name = path.Base(p)
			}
			joined := compactDiffRow{name: name, fullPath: p, afterType: row.typ, typ: row.typ, after: row.size, afterFiles: row.files, afterItems: row.items, afterMTime: row.mtime, firstChild: -1, nextSibling: -1, keep: true}
			rows = append(rows, joined)
			lookup[p] = int32(len(rows) - 1)
		}
	}
	if len(rows) > maxReportNodes {
		return nil, fmt.Errorf("comparison contains more than %d combined nodes", maxReportNodes)
	}
	r := &compactDiffReport{Complete: complete, StatusCounts: map[string]int{"added": 0, "removed": 0, "grown": 0, "shrunk": 0, "changed": 0, "unchanged": 0}, rows: rows, root: -1}
	rank := newDiffRankingsBuilder()
	for i := range r.rows {
		row := &r.rows[i]
		hasBefore := i < len(b.rows)
		hasAfter := row.keep
		typ := row.afterType
		if typ == compactTypeUnknown {
			typ = row.beforeType
		}
		if row.beforeType == compactTypeDir || row.afterType == compactTypeDir {
			typ = compactTypeDir
		}
		row.typ = typ
		status := compactStatusUnchanged
		switch {
		case !hasBefore:
			status = compactStatusAdded
		case !hasAfter:
			status = compactStatusRemoved
		case row.beforeType != row.afterType:
			status = compactStatusChanged
		case row.after > row.before:
			status = compactStatusGrown
		case row.after < row.before:
			status = compactStatusShrunk
		case row.beforeFiles != row.afterFiles || row.metadataChanged:
			status = compactStatusChanged
		}
		row.status = status
		row.keep = false
		rank.add(DiffRankingRow{Name: row.name, Path: row.fullPath, Type: typ.String(), Before: row.before, After: row.after, Delta: row.after - row.before, BeforeFiles: int(row.beforeFiles), AfterFiles: int(row.afterFiles), Status: status.String()})
		if status != compactStatusUnchanged {
			r.StatusCounts[status.String()]++
		}
	}
	// Determine union parent links directly from the compact input links. This
	// avoids a per-node child-set/map during the merge.
	parentOf := make([]int32, len(r.rows))
	for i := range parentOf {
		parentOf[i] = -1
	}
	for _, s := range []*compactSnapshot{b, a} {
		for _, n := range s.rows {
			pi, ok := lookup[cleanDiffRoot(n.fullPath)]
			if !ok {
				continue
			}
			for c := n.firstChild; c >= 0; c = s.rows[int(c)].nextSibling {
				ci, ok := lookup[cleanDiffRoot(s.rows[int(c)].fullPath)]
				if ok && int(ci) != r.root && parentOf[int(ci)] < 0 {
					parentOf[int(ci)] = pi
				}
			}
		}
	}
	r.root = int(lookup[br])
	// Sort each sibling group by path using numeric row references only.
	order := make([]int32, len(r.rows))
	for i := range order {
		order[i] = int32(i)
		r.rows[i].firstChild = -1
		r.rows[i].nextSibling = -1
	}
	sort.Slice(order, func(i, j int) bool { return r.rows[int(order[i])].fullPath < r.rows[int(order[j])].fullPath })
	lastChild := make([]int32, len(r.rows))
	for i := range lastChild {
		lastChild[i] = -1
	}
	for _, ci := range order {
		pi := parentOf[int(ci)]
		if pi < 0 || int(ci) == r.root {
			continue
		}
		if r.rows[int(pi)].firstChild < 0 {
			r.rows[int(pi)].firstChild = ci
		} else {
			r.rows[int(lastChild[int(pi)])].nextSibling = ci
		}
		lastChild[int(pi)] = ci
	}
	// Build an explicit root-reachable postorder. It remains correct when a new
	// current-only row is inserted below an older parent.
	type walkFrame struct{ id, next int32 }
	postorder := order[:0]
	stack := []walkFrame{{id: int32(r.root), next: r.rows[r.root].firstChild}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next >= 0 {
			child := top.next
			top.next = r.rows[int(child)].nextSibling
			stack = append(stack, walkFrame{id: child, next: r.rows[int(child)].firstChild})
			continue
		}
		postorder = append(postorder, top.id)
		stack = stack[:len(stack)-1]
	}
	if len(postorder) != len(r.rows) {
		return nil, fmt.Errorf("comparison contains nodes disconnected from root %q", br)
	}
	// All statistics and rankings are complete before optional output pruning.
	for _, id := range postorder {
		row := r.rows[int(id)]
		var bd, ad int64
		if row.beforeType == compactTypeDir || row.afterType == compactTypeDir {
			if row.beforeType == compactTypeDir {
				for c := row.firstChild; c >= 0; c = r.rows[int(c)].nextSibling {
					bd += r.rows[int(c)].before
				}
			}
			if row.afterType == compactTypeDir {
				for c := row.firstChild; c >= 0; c = r.rows[int(c)].nextSibling {
					ad += r.rows[int(c)].after
				}
			}
		}
		d := (row.after - ad) - (row.before - bd)
		if row.beforeType != compactTypeDir && row.afterType != compactTypeDir {
			d = row.after - row.before
		}
		if d > 0 {
			r.Increased += d
		}
		if d < 0 {
			r.Decreased -= d
		}
	}
	for _, id := range postorder {
		row := &r.rows[int(id)]
		row.keep = complete || row.status != compactStatusUnchanged
		if !row.keep {
			for c := row.firstChild; c >= 0; c = r.rows[int(c)].nextSibling {
				if r.rows[int(c)].keep {
					row.keep = true
					break
				}
			}
		}
	}
	r.Count = 1 // root is always serialized, even for an identical comparison
	for i, row := range r.rows {
		if i != r.root && row.keep {
			r.Count++
		}
	}
	order = nil
	parentOf = nil
	lastChild = nil
	lookup = nil // release the large path index before serialization begins
	r.Rankings = rank.report()
	r.Before = compactSnapshotInfo(b, bs)
	r.After = compactSnapshotInfo(a, as)
	return r, nil
}

func compactSnapshotInfo(s *compactSnapshot, source string) DiffSnapshot {
	root := s.rows[s.root]
	files, dirs := 0, 0
	for _, n := range s.rows {
		if n.typ == compactTypeDir {
			dirs++
		} else {
			files++
		}
	}
	return DiffSnapshot{Source: source, Root: cleanDiffRoot(root.fullPath), Size: root.size, Files: files, Directories: dirs, CapturedAt: s.meta.capturedAt}
}

func (r *compactDiffReport) WriteJSON(w io.Writer) error { return writeCompactDiffJSON(w, r) }

func writeCompactDiffJSON(w io.Writer, r *compactDiffReport) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	meta := struct {
		Before       DiffSnapshot   `json:"before"`
		After        DiffSnapshot   `json:"after"`
		Complete     bool           `json:"complete"`
		Increased    int64          `json:"increased"`
		Decreased    int64          `json:"decreased"`
		StatusCounts map[string]int `json:"statusCounts"`
		Rankings     DiffRankings   `json:"rankings"`
	}{r.Before, r.After, r.Complete, r.Increased, r.Decreased, r.StatusCounts, r.Rankings}
	buf, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if len(buf) == 0 || buf[len(buf)-1] != '}' {
		return errors.New("invalid compact diff metadata")
	}
	if _, err = bw.Write(buf[:len(buf)-1]); err != nil {
		return err
	}
	if _, err = bw.WriteString(",\"root\":"); err != nil {
		return err
	}
	if err = writeCompactNode(bw, r, r.root, 0); err != nil {
		return err
	}
	if _, err = bw.WriteString("}\n"); err != nil {
		return err
	}
	return bw.Flush()
}

func writeCompactNode(w *bufio.Writer, r *compactDiffReport, id, depth int) error {
	if id < 0 || id >= len(r.rows) {
		return errors.New("compact diff contains an invalid child reference")
	}
	if depth > compactMaxDepth {
		return errors.New("compact diff output exceeds maximum depth")
	}
	row := r.rows[id]
	n := DiffNode{Name: row.name, Path: row.fullPath, Type: row.typ.String(), BeforeType: row.beforeType.String(), AfterType: row.afterType.String(), Before: row.before, After: row.after, Delta: row.after - row.before, BeforeFiles: int(row.beforeFiles), AfterFiles: int(row.afterFiles), BeforeItems: int(row.beforeItems), AfterItems: int(row.afterItems), BeforeMTime: row.beforeMTime, AfterMTime: row.afterMTime, Status: row.status.String()}
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	last := int(row.firstChild)
	if !r.Complete {
		for last >= 0 && !r.rows[last].keep {
			last = int(r.rows[last].nextSibling)
		}
	}
	if last < 0 {
		return writeJSONBytes(w, data)
	}
	if len(data) == 0 || data[len(data)-1] != '}' {
		return errors.New("invalid compact diff node")
	}
	if _, err = w.Write(data[:len(data)-1]); err != nil {
		return err
	}
	if _, err = w.WriteString(`,"children":[`); err != nil {
		return err
	}
	first := true
	for c := int(row.firstChild); c >= 0; c = int(r.rows[c].nextSibling) {
		if !r.Complete && !r.rows[c].keep {
			continue
		}
		if !first {
			if _, err = w.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		if err = writeCompactNode(w, r, c, depth+1); err != nil {
			return err
		}
	}
	_, err = w.WriteString("]}")
	return err
}

func writeJSONBytes(w io.Writer, b []byte) error { _, err := w.Write(b); return err }
