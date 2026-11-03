package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	diffStreamCompatLimit = 16 << 20
	diffStreamDiskLimit   = int64(8 << 30)
	diffStreamDiskReserve = int64(256 << 20)
	diffStreamStringLimit = 64 << 10
	diffStreamNumberLimit = 64 << 10
	diffStreamDepthLimit  = 1024
	diffCompatPathBudget  = 64 << 20
)

type unsupportedNativeError struct{ reason string }

func (e *unsupportedNativeError) Error() string { return e.reason }

func unsupportedNative(reason string) error { return &unsupportedNativeError{reason: reason} }

var (
	diffNodesBucket    = []byte("nodes")
	diffChildrenBucket = []byte("children")
	diffStateBucket    = []byte("state")
)

var diffTempWriteCount atomic.Uint64

type diskDiffRecord struct {
	Depth int
	Node  DiffNode
}
type diskDiffReport struct {
	Before, After        DiffSnapshot
	Complete             bool
	Increased, Decreased int64
	StatusCounts         map[string]int
	Rankings             DiffRankings
	RecordsPath          string
	Count                int64
	dir                  string
	closed               bool
	mu                   sync.Mutex
}

func (r *diskDiffReport) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.dir != "" {
		return os.RemoveAll(r.dir)
	}
	return nil
}

type streamNode struct {
	Name, Path, Type, MTime, Flag, MIME string
	Size                                int64
	Items, Files                        int
	Parent                              string
	Own                                 int64
}

type diskNodeWriter struct {
	db                  *bolt.DB
	side                byte
	budget              int
	totalNodes          int
	rows                []streamNode
	bytes               int
	activePathBytes     int
	activeMetadataBytes int
	activeMetadataLimit int
}

func newDiskNodeWriter(db *bolt.DB, side byte, budget int) *diskNodeWriter {
	return &diskNodeWriter{db: db, side: side, budget: budget, activeMetadataLimit: diffCompatPathBudget, rows: make([]streamNode, 0, 1000)}
}
func (w *diskNodeWriter) add(node streamNode) error {
	if w.totalNodes >= w.budget {
		return fmt.Errorf("comparison contains more than %d combined nodes", maxReportNodes)
	}
	keyBytes := 1 + len(node.Path)
	childKeyBytes := 5 + len(node.Parent) + len(node.Path)
	if keyBytes > bolt.MaxKeySize || childKeyBytes > bolt.MaxKeySize {
		return fmt.Errorf("node path %q exceeds bbolt's %d-byte key limit", node.Path, bolt.MaxKeySize)
	}
	raw, e := json.Marshal(node)
	if e != nil {
		return e
	}
	if len(raw) > diffStreamRecordLimit {
		return fmt.Errorf("node exceeds the %d-byte record limit", diffStreamRecordLimit)
	}
	cost := len(raw) + len(node.Path) + len(node.Parent) + 16
	if len(w.rows) > 0 && (len(w.rows) >= 1000 || w.bytes+cost > 16<<20) {
		if e = w.flush(); e != nil {
			return e
		}
	}
	if cost > 16<<20 {
		return fmt.Errorf("single database record exceeds the 16 MiB transaction limit")
	}
	w.rows = append(w.rows, node)
	w.bytes += cost
	w.totalNodes++
	if len(w.rows) >= 1000 || w.bytes >= 15<<20 {
		return w.flush()
	}
	return nil
}
func (w *diskNodeWriter) flush() error {
	if len(w.rows) == 0 {
		return nil
	}
	rows := w.rows
	err := w.db.Update(func(tx *bolt.Tx) error {
		nodes := tx.Bucket(diffNodesBucket)
		batchSeen := make(map[string]struct{}, len(rows))
		for _, r := range rows {
			if _, ok := batchSeen[r.Path]; ok {
				return fmt.Errorf("snapshot has duplicate or ambiguous path %q", r.Path)
			}
			batchSeen[r.Path] = struct{}{}
			key := append([]byte{w.side}, []byte(r.Path)...)
			if nodes.Get(key) != nil {
				return fmt.Errorf("snapshot has duplicate or ambiguous path %q", r.Path)
			}
			raw, e := json.Marshal(r)
			if e != nil {
				return e
			}
			if e = nodes.Put(key, raw); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.rows = make([]streamNode, 0, 1000)
	w.bytes = 0
	return checkDiffTempBudget(filepath.Dir(w.db.Path()))
}

func buildDiskDiffReport(beforeSource, afterSource string, stdin io.Reader, complete bool) (_ *diskDiffReport, retErr error) {
	dir, err := os.MkdirTemp("", "webdiskstat-diff-")
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0700)
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	db, err := bolt.Open(filepath.Join(dir, "diff.db"), 0600, &bolt.Options{Timeout: time.Second, NoFreelistSync: true, InitialMmapSize: 64 << 20})
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := db.Close(); retErr == nil && e != nil {
			retErr = e
		}
	}()
	if err = db.Update(func(tx *bolt.Tx) error {
		for _, n := range [][]byte{diffNodesBucket, diffChildrenBucket, diffStateBucket} {
			if _, e := tx.CreateBucketIfNotExists(n); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	before, err := ingestDiskSnapshot(db, 0, beforeSource, stdin, dir)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	after, err := ingestDiskSnapshot(db, 1, afterSource, stdin, dir)
	if err != nil {
		return nil, fmt.Errorf("current: %w", err)
	}
	if err = validateSnapshotParents(db, 0, before.Root); err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	if err = validateSnapshotParents(db, 1, after.Root); err != nil {
		return nil, fmt.Errorf("current: %w", err)
	}
	if before.Files+before.Directories+after.Files+after.Directories > maxReportNodes {
		return nil, fmt.Errorf("comparison contains more than %d combined nodes", maxReportNodes)
	}
	if err = checkDiffTempBudget(dir); err != nil {
		return nil, err
	}
	if before.Root == "" || after.Root == "" {
		return nil, fmt.Errorf("snapshot root is unavailable")
	}
	if before.Root != after.Root {
		return nil, fmt.Errorf("snapshot roots differ: baseline %q, current %q", before.Root, after.Root)
	}
	statusCounts := map[string]int{"added": 0, "removed": 0, "grown": 0, "shrunk": 0, "changed": 0, "unchanged": 0}
	rankings := newDiffRankingsBuilder()
	var increased, decreased int64
	statusCounts, rankings, increased, decreased, err = compareDiskSnapshots(db, complete)
	if err != nil {
		return nil, err
	}
	if err = markChangedAncestors(db); err != nil {
		return nil, err
	}
	recordsPath := filepath.Join(dir, "records.jsonl")
	out, err := os.OpenFile(recordsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	count, err := writeDiskRecords(db, &diffBudgetWriter{w: out, dir: dir}, complete, before.Root)
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = checkDiffTempBudget(dir); err != nil {
		return nil, err
	}
	rep := &diskDiffReport{Before: before, After: after, Complete: complete, Increased: increased, Decreased: decreased, StatusCounts: statusCounts, Rankings: rankings.report(), RecordsPath: recordsPath, Count: count, dir: dir}
	cleanup = false
	return rep, nil
}

type diskDiffState struct {
	Node   DiffNode
	Parent string
	Keep   bool
}

type diskCompareRow struct {
	key, value []byte
	path       string
	parent     string
}

func compareDiskSnapshots(db *bolt.DB, complete bool) (map[string]int, *diffRankingsBuilder, int64, int64, error) {
	statuses := map[string]int{"added": 0, "removed": 0, "grown": 0, "shrunk": 0, "changed": 0, "unchanged": 0}
	rankings := newDiffRankingsBuilder()
	var increased, decreased int64
	lastPath := ""
	for {
		batch := make([]diskCompareRow, 0, 1000)
		batchBytes, exhausted := 0, false
		err := db.View(func(tx *bolt.Tx) error {
			bucket := tx.Bucket(diffNodesBucket)
			bc, ac := bucket.Cursor(), bucket.Cursor()
			bk, bv := bc.Seek(append([]byte{0}, []byte(lastPath)...))
			ak, av := ac.Seek(append([]byte{1}, []byte(lastPath)...))
			if bk != nil && bk[0] != 0 {
				bk = nil
				bv = nil
			}
			if ak != nil && ak[0] != 1 {
				ak = nil
				av = nil
			}
			if lastPath != "" {
				if bk != nil && string(bk[1:]) == lastPath {
					bk, bv = bc.Next()
				}
				if bk != nil && bk[0] != 0 {
					bk = nil
					bv = nil
				}
				if ak != nil && string(ak[1:]) == lastPath {
					ak, av = ac.Next()
				}
				if ak != nil && ak[0] != 1 {
					ak = nil
					av = nil
				}
			}
			for len(batch) < 1000 && batchBytes < (13<<20) {
				if bk == nil && ak == nil {
					exhausted = true
					break
				}
				var bp, ap []byte
				var bn, an streamNode
				if bk == nil {
					ap = bytes.Clone(ak)
					if e := json.Unmarshal(av, &an); e != nil {
						return e
					}
					ak, av = ac.Next()
					if ak != nil && ak[0] != 1 {
						ak = nil
						av = nil
					}
				} else if ak == nil {
					bp = bytes.Clone(bk)
					if e := json.Unmarshal(bv, &bn); e != nil {
						return e
					}
					bk, bv = bc.Next()
					if bk != nil && bk[0] != 0 {
						bk = nil
						bv = nil
					}
				} else {
					c := bytes.Compare(bk[1:], ak[1:])
					if c <= 0 {
						bp = bytes.Clone(bk)
						if e := json.Unmarshal(bv, &bn); e != nil {
							return e
						}
						bk, bv = bc.Next()
						if bk != nil && bk[0] != 0 {
							bk = nil
							bv = nil
						}
					}
					if c >= 0 {
						ap = bytes.Clone(ak)
						if e := json.Unmarshal(av, &an); e != nil {
							return e
						}
						ak, av = ac.Next()
						if ak != nil && ak[0] != 1 {
							ak = nil
							av = nil
						}
					}
				}
				p := ""
				if len(bp) > 1 {
					p = string(bp[1:])
				}
				if p == "" && len(ap) > 1 {
					p = string(ap[1:])
				}
				lastPath = p
				bt, at := bn.Type, an.Type
				typ := at
				if typ == "" {
					typ = bt
				}
				if bt == "dir" || at == "dir" {
					typ = "dir"
				}
				status := "unchanged"
				switch {
				case len(bp) == 0:
					status = "added"
				case len(ap) == 0:
					status = "removed"
				case bt != at:
					status = "changed"
				case an.Size > bn.Size:
					status = "grown"
				case an.Size < bn.Size:
					status = "shrunk"
				case bn.Files != an.Files || bn.MTime != an.MTime || bn.Flag != an.Flag || bn.MIME != an.MIME:
					status = "changed"
				}
				name := an.Name
				if name == "" {
					name = bn.Name
				}
				if name == "" {
					name = path.Base(p)
				}
				delta := an.Size - bn.Size
				rankings.add(DiffRankingRow{Name: name, Path: p, Type: typ, Before: bn.Size, After: an.Size, Delta: delta, BeforeFiles: bn.Files, AfterFiles: an.Files, Status: status})
				if status != "unchanged" {
					statuses[status]++
				}
				cd := an.Own - bn.Own
				if cd > 0 {
					increased += cd
				}
				if cd < 0 {
					decreased -= cd
				}
				node := DiffNode{Name: name, Path: p, Type: typ, BeforeType: bt, AfterType: at, Before: bn.Size, After: an.Size, Delta: delta, BeforeFiles: bn.Files, AfterFiles: an.Files, BeforeItems: bn.Items, AfterItems: an.Items, BeforeMTime: bn.MTime, AfterMTime: an.MTime, Status: status}
				parent := an.Parent
				if parent == "" {
					parent = bn.Parent
				}
				raw, e := json.Marshal(diskDiffState{Node: node, Parent: parent, Keep: status != "unchanged" || complete})
				if e != nil {
					return e
				}
				if len(raw) > diffStreamRecordLimit {
					return fmt.Errorf("comparison record exceeds %d-byte limit", diffStreamRecordLimit)
				}
				batch = append(batch, diskCompareRow{key: append([]byte{2}, []byte(p)...), value: raw, path: p, parent: parent})
				batchBytes += len(raw) + len(p) + 16
			}
			return nil
		})
		if err != nil {
			return nil, nil, 0, 0, err
		}
		if len(batch) == 0 && exhausted {
			return statuses, rankings, increased, decreased, nil
		}
		if len(batch) == 0 {
			return nil, nil, 0, 0, fmt.Errorf("diff comparison made no progress")
		}
		err = db.Update(func(tx *bolt.Tx) error {
			s := tx.Bucket(diffStateBucket)
			children := tx.Bucket(diffChildrenBucket)
			nodes := tx.Bucket(diffNodesBucket)
			for _, row := range batch {
				if e := s.Put(row.key, row.value); e != nil {
					return e
				}
				if e := children.Put(childIndexKey(2, row.parent, row.path), nil); e != nil {
					return e
				}
				for _, side := range []byte{0, 1} {
					if e := nodes.Delete(append([]byte{side}, []byte(row.path)...)); e != nil {
						return e
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, 0, 0, err
		}
		if err = checkDiffTempBudget(filepath.Dir(db.Path())); err != nil {
			return nil, nil, 0, 0, err
		}
		if exhausted {
			return statuses, rankings, increased, decreased, nil
		}
	}
}

func validateSnapshotParents(db *bolt.DB, side byte, rootPath string) error {
	return db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(diffNodesBucket)
		c := b.Cursor()
		roots := 0
		for k, v := c.Seek([]byte{side}); k != nil && k[0] == side; k, v = c.Next() {
			var node streamNode
			if e := json.Unmarshal(v, &node); e != nil {
				return e
			}
			if len(k) < 2 || string(k[1:]) != node.Path || path.Clean(node.Path) != node.Path {
				return fmt.Errorf("invalid or ambiguous node path %q", node.Path)
			}
			if node.Parent == "" {
				roots++
				if node.Path != rootPath {
					return fmt.Errorf("unexpected root path %q", node.Path)
				}
				continue
			}
			if node.Parent == node.Path {
				return fmt.Errorf("node %q is its own parent", node.Path)
			}
			parent := b.Get(append([]byte{side}, []byte(node.Parent)...))
			if parent == nil {
				return fmt.Errorf("node %q references missing parent %q", node.Path, node.Parent)
			}
		}
		if roots != 1 {
			return fmt.Errorf("snapshot has %d roots, want exactly one", roots)
		}
		return nil
	})
}

func countSnapshotNodes(db *bolt.DB, side byte) (int, error) {
	count := 0
	err := db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(diffNodesBucket).Cursor()
		for k, _ := c.Seek([]byte{side}); k != nil && k[0] == side; k, _ = c.Next() {
			count++
			if count > maxReportNodes {
				return fmt.Errorf("comparison contains more than %d combined nodes", maxReportNodes)
			}
		}
		return nil
	})
	return count, err
}

func markChangedAncestors(db *bolt.DB) error {
	lastPath := ""
	for {
		paths := make([]string, 0, 1000)
		pathsBytes := 0
		exhausted := false
		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(diffStateBucket)
			c := b.Cursor()
			seek := append([]byte{2}, []byte(lastPath)...)
			k, v := c.Seek(seek)
			if lastPath != "" && k != nil && string(k[1:]) == lastPath {
				k, v = c.Next()
			}
			for k != nil && k[0] == 2 && len(paths) < 1000 && pathsBytes < 13<<20 {
				kcopy := bytes.Clone(k)
				var rec diskDiffState
				if e := json.Unmarshal(v, &rec); e != nil {
					return e
				}
				lastPath = string(kcopy[1:])
				if rec.Node.Status != "unchanged" {
					paths = append(paths, lastPath)
					pathsBytes += len(lastPath)
				}
				k, v = c.Next()
			}
			if k == nil || k[0] != 2 {
				exhausted = true
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, changed := range paths {
			err = db.Update(func(tx *bolt.Tx) error {
				s := tx.Bucket(diffStateBucket)
				v := s.Get(append([]byte{2}, []byte(changed)...))
				if v == nil {
					return fmt.Errorf("missing diff record for %q", changed)
				}
				var current diskDiffState
				if e := json.Unmarshal(v, &current); e != nil {
					return e
				}
				p := current.Parent
				seen := make(map[[32]byte]struct{}, 16)
				ops, bytesWritten := 0, 0
				for p != "" {
					hash := sha256.Sum256([]byte(p))
					if _, ok := seen[hash]; ok {
						return fmt.Errorf("diff parent cycle at %q", p)
					}
					seen[hash] = struct{}{}
					if len(seen) > diffStreamDepthLimit {
						return fmt.Errorf("diff parent chain exceeds depth %d", diffStreamDepthLimit)
					}
					key := append([]byte{2}, []byte(p)...)
					pv := s.Get(key)
					if pv == nil {
						return fmt.Errorf("diff parent %q is missing", p)
					}
					var parent diskDiffState
					if e := json.Unmarshal(pv, &parent); e != nil {
						return e
					}
					if parent.Keep {
						break
					}
					parent.Keep = true
					raw, e := json.Marshal(parent)
					if e != nil {
						return e
					}
					ops++
					bytesWritten += len(raw)
					if ops > 1000 || bytesWritten > 16<<20 {
						return fmt.Errorf("ancestor marking exceeds bounded transaction limits")
					}
					if e = s.Put(key, raw); e != nil {
						return e
					}
					p = parent.Parent
				}
				return nil
			})
			if err != nil {
				return err
			}
			if diffTempWriteCount.Add(1)%8 == 0 {
				if err = checkDiffTempBudget(filepath.Dir(db.Path())); err != nil {
					return err
				}
			}
		}
		if exhausted {
			return nil
		}
		if len(paths) == 0 && lastPath == "" {
			return fmt.Errorf("ancestor scan made no progress")
		}
	}
}

func ingestDiskSnapshot(db *bolt.DB, side byte, source string, stdin io.Reader, tmpdir string) (DiffSnapshot, error) {
	var snap DiffSnapshot
	snap.Source = source
	budget := maxReportNodes
	if side == 1 {
		used, e := countSnapshotNodes(db, 0)
		if e != nil {
			return snap, e
		}
		budget -= used
	}
	input, err := openDiskInput(source, stdin, tmpdir)
	if err != nil {
		return snap, err
	}
	defer func() {
		name := input.Name()
		_ = input.Close()
		_ = os.Remove(name)
	}()
	if err = validateDiffLexical(input); err != nil {
		return snap, err
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return snap, err
	}
	dec := json.NewDecoder(bufio.NewReader(input))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil {
		return snap, err
	}
	if first == json.Delim('[') {
		major, e := dec.Token()
		if e != nil {
			return snap, e
		}
		minor, e := dec.Token()
		if e != nil {
			return snap, e
		}
		metaToken, e := dec.Token()
		if e != nil {
			return snap, e
		}
		if majorNum, ok := major.(json.Number); ok {
			if _, ok = minor.(json.Number); ok {
				if metaToken == json.Delim('{') {
					meta, e := readNativeInfo(dec)
					if e != nil {
						var unsupported *unsupportedNativeError
						if errors.As(e, &unsupported) {
							return ingestDiskCompatibility(db, input, source, side, budget)
						}
						return snap, e
					}
					if scanner := scannerMetadataIdentity(meta); scanner != "" && !strings.EqualFold(scanner, "gdu") {
						return snap, scannerIdentityError(scanner)
					}
					metadataRoot := metadataValue(meta, "root", "Root", "path", "Path", "root_path", "rootPath")
					rootFallback := metadataRoot
					capturedAt := metadataValue(meta, "timestamp", "Timestamp", "capturedAt", "captured_at", "scan_time", "scanTime")
					meta = nil
					tree, e := dec.Token()
					if e != nil {
						return snap, e
					}
					if tree == json.Delim('[') {
						if rootFallback == "" {
							rootFallback = "root"
						}
						writer := newDiskNodeWriter(db, side, budget)
						root, e := parseNativeSequence(dec, side, "", rootFallback, true, writer)
						if e != nil {
							var unsupported *unsupportedNativeError
							if !errors.As(e, &unsupported) {
								return snap, e
							}
							if e = deleteDiskSnapshotSide(db, side); e != nil {
								return snap, e
							}
							if _, e = input.Seek(0, io.SeekStart); e != nil {
								return snap, e
							}
							return ingestDiskCompatibility(db, input, source, side, budget)
						}
						if e = writer.flush(); e != nil {
							return snap, e
						}
						outer, e := dec.Token()
						if e != nil || outer != json.Delim(']') {
							return snap, fmt.Errorf("invalid native GDU export wrapper")
						}
						if _, e = dec.Token(); e != io.EOF {
							if e == nil {
								return snap, fmt.Errorf("input contains multiple JSON values")
							}
							return snap, e
						}
						rootPath := metadataRoot
						if rootPath == "" {
							rootPath = root.Path
						}
						if rootPath == "root" {
							return snap, fmt.Errorf("gdu snapshot root path is unavailable")
						}
						if cleanDiffRoot(rootPath) != cleanDiffRoot(root.Path) {
							return snap, fmt.Errorf("gdu metadata root %q does not match tree root %q", rootPath, root.Path)
						}
						snap = DiffSnapshot{Source: source, Root: path.Clean(root.Path), Size: root.Size, Files: root.Files, Directories: root.Directories, CapturedAt: capturedAt}
						return snap, nil
					}
					_ = majorNum
				}
			}
		}
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return snap, err
	}
	return ingestDiskCompatibility(db, input, source, side, budget)
}

func ingestDiskCompatibility(db *bolt.DB, input *os.File, source string, side byte, budget int) (DiffSnapshot, error) {
	var snap DiffSnapshot
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return snap, err
	}
	all, readErr := io.ReadAll(io.LimitReader(input, diffStreamCompatLimit+1))
	if readErr != nil {
		return snap, readErr
	}
	if len(all) > diffStreamCompatLimit {
		return snap, fmt.Errorf("unsupported non-native export exceeds %d MiB compatibility limit; re-export with native GDU JSON format", diffStreamCompatLimit>>20)
	}
	var raw any
	d := json.NewDecoder(bytes.NewReader(all))
	d.UseNumber()
	if err := d.Decode(&raw); err != nil {
		return snap, err
	}
	var trailing any
	if e := d.Decode(&trailing); e != io.EOF {
		if e == nil {
			return snap, fmt.Errorf("input contains multiple JSON values")
		}
		return snap, e
	}
	if err := preflightDiffNormalization(raw, diffCompatPathBudget, maxReportNodes); err != nil {
		return snap, err
	}
	root, meta, e := normalizeGDUForDiff(raw)
	if e != nil {
		return snap, e
	}
	snap = snapshotInfo(root, source)
	snap.CapturedAt = meta.capturedAt
	if err := insertLegacyTree(db, side, root, budget); err != nil {
		return snap, err
	}
	return snap, nil
}

// preflightDiffNormalization estimates the total full-path storage before the
// compatibility normalizer allocates Nodes and path strings. It traverses the
// already bounded JSON value but never constructs paths or retains node state.
func preflightDiffNormalization(raw any, pathBudget, nodeBudget int) error {
	usedPaths, nodes := 0, 0
	var walk func(any, int, int) error
	walk = func(value any, parentPathLen, fallbackLen int) error {
		nodes++
		if nodes > nodeBudget {
			return fmt.Errorf("compatibility export exceeds the %d-node preflight limit", nodeBudget)
		}
		// Count every JSON value conservatively: primitives can become nodes,
		// and generated item-N fallback names must not be treated as empty.
		nameLen := max(fallbackLen, 32)
		pathLen := parentPathLen + nameLen + 1
		switch v := value.(type) {
		case map[string]any:
			for _, key := range nameKeys {
				if s, ok := v[key].(string); ok && s != "" {
					nameLen = len(s)
					break
				}
			}
			for _, key := range pathKeys {
				if s, ok := v[key].(string); ok && s != "" {
					pathLen = len(s)
					if pathLen == nameLen && parentPathLen > 0 {
						pathLen += parentPathLen + 1
					}
					break
				}
			}
		case []any:
			if len(v) > 0 {
				if info, ok := v[0].(map[string]any); ok {
					for _, key := range nameKeys {
						if s, ok := info[key].(string); ok && s != "" {
							nameLen = len(s)
							break
						}
					}
					for _, key := range pathKeys {
						if s, ok := info[key].(string); ok && s != "" {
							pathLen = len(s)
							break
						}
					}
				}
			}
		}
		// Explicit absolute paths may ignore their parents, but summing parent
		// and name is a safe upper bound for all relative/fallback forms.
		pathLen = max(pathLen, parentPathLen+nameLen+1)
		if scalar, ok := value.(string); ok {
			pathLen = max(pathLen, parentPathLen+len(scalar)+1)
		}
		usedPaths += pathLen
		if usedPaths > pathBudget {
			return fmt.Errorf("compatibility export normalized paths exceed the %d MiB memory budget", pathBudget>>20)
		}
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				childFallback := len(key)
				if err := walk(child, pathLen+1, childFallback); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child, pathLen+1, 0); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(raw, 0, 4)
}

func deleteDiskSnapshotSide(db *bolt.DB, side byte) error {
	for _, bucketName := range [][]byte{diffNodesBucket, diffChildrenBucket} {
		bucket := bucketName
		for {
			var keys [][]byte
			err := db.View(func(tx *bolt.Tx) error {
				c := tx.Bucket(bucket).Cursor()
				for k, _ := c.Seek([]byte{side}); k != nil && k[0] == side && len(keys) < 1000; k, _ = c.Next() {
					keys = append(keys, bytes.Clone(k))
				}
				return nil
			})
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				break
			}
			if err = db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket(bucket)
				for _, key := range keys {
					if e := b.Delete(key); e != nil {
						return e
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// The lexical pass bounds individual JSON strings and nesting before encoding/json
// can allocate an unbounded token. It deliberately treats escapes conservatively.
func validateDiffLexical(r io.Reader) error {
	br := bufio.NewReader(r)
	quoted, escape := false, false
	stringBytes, depth := 0, 0
	inNumber, numberBytes := false, 0
	var total int64
	for {
		b, e := br.ReadByte()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		total++
		if total > maxDecompressedBytes {
			return errInputTooLarge
		}
		if quoted {
			if escape {
				escape = false
				stringBytes++
			} else if b == '\\' {
				escape = true
				stringBytes++
			} else if b == '"' {
				quoted = false
			} else {
				stringBytes++
			}
			if stringBytes > diffStreamStringLimit {
				return fmt.Errorf("JSON string exceeds %d-byte limit", diffStreamStringLimit)
			}
			continue
		}
		if b == '"' {
			quoted = true
			stringBytes = 0
			continue
		}
		if inNumber {
			if (b >= '0' && b <= '9') || b == '-' || b == '+' || b == '.' || b == 'e' || b == 'E' {
				numberBytes++
				if numberBytes > diffStreamNumberLimit {
					return fmt.Errorf("JSON number exceeds %d-byte limit", diffStreamNumberLimit)
				}
				continue
			}
			inNumber = false
			numberBytes = 0
		}
		if b == '-' || (b >= '0' && b <= '9') {
			inNumber = true
			numberBytes = 1
		}
		if b == '{' || b == '[' {
			depth++
			if depth > diffStreamDepthLimit {
				return fmt.Errorf("JSON nesting exceeds depth %d", diffStreamDepthLimit)
			}
		} else if b == '}' || b == ']' {
			depth--
			if depth < 0 {
				return fmt.Errorf("malformed JSON nesting")
			}
		}
	}
	if quoted || depth != 0 {
		return fmt.Errorf("malformed or truncated JSON input")
	}
	return nil
}

func readNativeInfo(dec *json.Decoder) (map[string]any, error) {
	m := make(map[string]any)
	bytesInRecord := 2
	for dec.More() {
		t, e := dec.Token()
		if e != nil {
			return nil, e
		}
		key, ok := t.(string)
		if !ok {
			return nil, fmt.Errorf("invalid native node metadata")
		}
		v, e := dec.Token()
		if e != nil {
			return nil, e
		}
		if _, ok := v.(json.Delim); ok {
			return nil, unsupportedNative(fmt.Sprintf("unsupported nested native node metadata (%q)", key))
		}
		bytesInRecord += len(key) + 4
		switch scalar := v.(type) {
		case string:
			bytesInRecord += len(scalar)
		case json.Number:
			bytesInRecord += len(scalar)
		default:
			bytesInRecord += 8
		}
		if bytesInRecord > diffStreamRecordLimit {
			return nil, fmt.Errorf("native GDU node metadata exceeds the %d-byte record limit", diffStreamRecordLimit)
		}
		m[key] = v
	}
	if _, e := dec.Token(); e != nil {
		return nil, e
	}
	return m, nil
}

type nativeTotals struct {
	Name, Path, Type, MTime, Flag, MIME string
	Size, Own                           int64
	Files, Items, Directories           int
	Parent                              string
}

func parseNativeSequence(dec *json.Decoder, side byte, parent, fallback string, root bool, writer *diskNodeWriter) (nativeTotals, error) {
	open, e := dec.Token()
	if e != nil || open != json.Delim('{') {
		return nativeTotals{}, unsupportedNative("native directory sequence does not begin with an info object")
	}
	info, e := readNativeInfo(dec)
	if e != nil {
		return nativeTotals{}, e
	}
	name, pathValue := firstString(info, nameKeys), firstString(info, pathKeys)
	own := firstNumber(info, sizeKeys)
	mt := firstScalar(info, mtimeKeys)
	flag := firstString(info, flagKeys)
	mimeValue := firstString(info, mimeKeys)
	info = nil // Do not retain arbitrary per-node metadata while descending.
	if pathValue != "" && (name == "" || name == pathValue) {
		name = displayNameFromPath(pathValue)
	}
	if name == "" {
		name = fallback
	}
	if name == "" {
		name = "root"
	}
	if pathValue == "" {
		pathValue = makePath(parent, name)
	} else if parent != "" && pathValue == name {
		pathValue = makePath(parent, name)
	}
	pathValue = path.Clean(pathValue)
	metadataBytes := len(name) + len(pathValue) + len(mt) + len(flag) + len(mimeValue)
	if err := checkNativeActiveMetadata(writer.activeMetadataBytes, metadataBytes, writer.activeMetadataLimit); err != nil {
		return nativeTotals{}, err
	}
	writer.activeMetadataBytes += metadataBytes
	defer func() { writer.activeMetadataBytes -= metadataBytes }()
	if len(pathValue) > diffCompatPathBudget-writer.activePathBytes {
		return nativeTotals{}, fmt.Errorf("native GDU active path metadata exceeds %d MiB; re-export with shorter paths", diffCompatPathBudget>>20)
	}
	writer.activePathBytes += len(pathValue)
	defer func() { writer.activePathBytes -= len(pathValue) }()
	size := own
	files, items, dirs := 0, 0, 1
	childTotal := int64(0)
	isDir := true
	childIndex := 0
	for dec.More() {
		tok, e := dec.Token()
		if e != nil {
			return nativeTotals{}, e
		}
		var child nativeTotals
		switch tok {
		case json.Delim('['):
			child, e = parseNativeSequence(dec, side, pathValue, fmt.Sprintf("item-%d", childIndex), false, writer)
		case json.Delim('{'):
			ci, x := readNativeInfo(dec)
			e = x
			if e == nil {
				child, e = storeNativeFile(writer, pathValue, fmt.Sprintf("item-%d", childIndex), ci)
			}
		default:
			e = unsupportedNative(fmt.Sprintf("unsupported native child value %T", tok))
		}
		if e != nil {
			return nativeTotals{}, e
		}
		childTotal += child.Size
		files += child.Files
		items += child.Items + 1
		dirs += child.Directories
		childIndex++
	}
	if items+1 > maxReportNodes {
		return nativeTotals{}, fmt.Errorf("report contains more than %d nodes", maxReportNodes)
	}
	if _, e = dec.Token(); e != nil {
		return nativeTotals{}, e
	}
	// Sequence form denotes a directory, including an empty sequence.
	size = own + childTotal
	if mimeValue != "" {
		mimeValue = strings.Split(mimeValue, ";")[0]
	}
	if childIndex > 0 {
		// The compatibility normalizer intentionally strips MIME from directories.
		mimeValue = ""
	}
	if !root && parent == "" {
		isDir = false
	}
	typ := "dir"
	if !isDir {
		typ = "file"
		files = 1
		dirs = 0
		items = 0
	}
	if root {
		items += 0
	}
	r := streamNode{Name: name, Path: pathValue, Type: typ, Size: size, Own: own, Files: files, Items: items, MTime: mt, Flag: flag, MIME: mimeValue, Parent: parent}
	if e = writer.add(r); e != nil {
		return nativeTotals{}, e
	}
	return nativeTotals{Name: name, Path: pathValue, Type: typ, Size: size, Own: own, Files: files, Items: items, Directories: dirs, Parent: parent}, nil
}

func checkNativeActiveMetadata(current, next, limit int) error {
	if current > limit || next > limit-current {
		return fmt.Errorf("native GDU active path metadata exceeds %d MiB; re-export with shorter metadata", limit>>20)
	}
	return nil
}

func storeNativeFile(writer *diskNodeWriter, parent, fallback string, info map[string]any) (nativeTotals, error) {
	name, pathValue := firstString(info, nameKeys), firstString(info, pathKeys)
	if pathValue != "" && (name == "" || name == pathValue) {
		name = displayNameFromPath(pathValue)
	}
	if name == "" {
		name = fallback
	}
	if pathValue == "" {
		pathValue = makePath(parent, name)
	} else if parent != "" && pathValue == name {
		pathValue = makePath(parent, name)
	}
	pathValue = path.Clean(pathValue)
	ext := extensionFor(name)
	isDir := firstBool(info, dirKeys)
	mt := ""
	if !isDir {
		mt = firstString(info, mimeKeys)
		if mt != "" {
			mt = strings.Split(mt, ";")[0]
		} else if v := mime.TypeByExtension(ext); v != "" {
			mt = strings.Split(v, ";")[0]
		}
	}
	size := firstNumber(info, sizeKeys)
	typ, files, directories := "file", 1, 0
	if isDir {
		typ, files, directories = "dir", 0, 1
	}
	r := streamNode{Name: name, Path: pathValue, Type: typ, Size: size, Own: size, Files: files, MTime: firstScalar(info, mtimeKeys), Flag: firstString(info, flagKeys), MIME: mt, Parent: parent}
	if e := writer.add(r); e != nil {
		return nativeTotals{}, e
	}
	return nativeTotals{Name: name, Path: pathValue, Type: typ, Size: size, Own: size, Files: files, Directories: directories, Parent: parent}, nil
}

func openDiskInput(source string, stdin io.Reader, dir string) (*os.File, error) {
	var in io.Reader
	if source == "-" {
		in = stdin
		if in == nil {
			return nil, errNoInput
		}
	} else {
		f, e := os.Open(source)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		in = f
	}
	spool, e := os.CreateTemp(dir, "input-*.json")
	if e != nil {
		return nil, e
	}
	_ = spool.Chmod(0600)
	limited := newSizeLimitedReader(in, maxInputBytes)
	br := bufio.NewReader(limited)
	head, _ := br.Peek(2)
	gz := filepath.Ext(source) == ".gz" || len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b
	var expanded io.Reader = br
	if gz {
		g, er := gzip.NewReader(br)
		if er != nil {
			spool.Close()
			return nil, er
		}
		defer g.Close()
		expanded = newSizeLimitedReader(g, maxDecompressedBytes)
	}
	if !gz {
		expanded = newSizeLimitedReader(br, maxDecompressedBytes)
	}
	if e = copyDiffInput(spool, expanded, dir); e != nil {
		spool.Close()
		return nil, e
	}
	if e = spool.Sync(); e != nil {
		spool.Close()
		return nil, e
	}
	_, e = spool.Seek(0, io.SeekStart)
	return spool, e
}

func copyDiffInput(dst *os.File, src io.Reader, dir string) error {
	buf := make([]byte, 1<<20)
	var sinceCheck int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if writeErr := writeAll(dst, buf[:n]); writeErr != nil {
				return writeErr
			}
			sinceCheck += int64(n)
			if sinceCheck >= 16<<20 {
				if e := checkDiffTempBudget(dir); e != nil {
					return e
				}
				sinceCheck = 0
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return checkDiffTempBudget(dir)
}

func checkDiffTempBudget(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if total > diffStreamDiskLimit-diffStreamDiskReserve {
				return fmt.Errorf("diff temporary files exceed the %d GiB disk budget", diffStreamDiskLimit>>30)
			}
		}
	}
	return nil
}

type diffBudgetWriter struct {
	w       io.Writer
	dir     string
	pending int64
}

func (w *diffBudgetWriter) Write(p []byte) (int, error) {
	if err := writeAll(w.w, p); err != nil {
		return 0, err
	}
	n := len(p)
	w.pending += int64(n)
	if w.pending >= 16<<20 {
		if e := checkDiffTempBudget(w.dir); e != nil {
			return n, e
		}
		w.pending = 0
	}
	return n, nil
}

func insertLegacyTree(db *bolt.DB, side byte, root *Node, budget int) error {
	writer := newDiskNodeWriter(db, side, budget)
	count := 0
	var walk func(*Node, string) error
	walk = func(n *Node, parent string) error {
		count++
		if count > maxReportNodes {
			return fmt.Errorf("comparison contains more than %d nodes", maxReportNodes)
		}
		r := streamNode{Name: n.Name, Path: path.Clean(n.Path), Type: n.Type, Size: n.Size, Items: n.Items, Files: n.Files, MTime: n.MTime, Flag: n.Flag, MIME: n.MIME, Parent: parent}
		childSum := int64(0)
		for _, c := range n.Children {
			childSum += c.Size
		}
		r.Own = n.Size
		if n.Type == "dir" {
			r.Own -= childSum
		}
		if e := func() error {
			for _, c := range n.Children {
				if e := walk(c, r.Path); e != nil {
					return e
				}
			}
			return nil
		}(); e != nil {
			return e
		}
		return writer.add(r)
	}
	if e := walk(root, ""); e != nil {
		return e
	}
	return writer.flush()
}

func childIndexKey(side byte, parent, child string) []byte {
	key := make([]byte, 5+len(parent)+len(child))
	key[0] = side
	binary.BigEndian.PutUint32(key[1:5], uint32(len(parent)))
	copy(key[5:], parent)
	copy(key[5+len(parent):], child)
	return key
}

func writeDiskRecords(db *bolt.DB, w io.Writer, complete bool, rootPath string) (int64, error) {
	var count int64
	err := db.View(func(tx *bolt.Tx) error {
		s := tx.Bucket(diffStateBucket)
		key := append([]byte{2}, []byte(rootPath)...)
		var st diskDiffState
		if e := json.Unmarshal(s.Get(key), &st); e != nil {
			return fmt.Errorf("comparison root record missing: %w", e)
		}
		expected := int64(0)
		stateCursor := s.Cursor()
		for k, _ := stateCursor.Seek([]byte{2}); k != nil && k[0] == 2; k, _ = stateCursor.Next() {
			expected++
		}
		type frame struct {
			path    string
			depth   int
			started bool
			seeked  bool
			cursor  *bolt.Cursor
			prefix  []byte
		}
		stack := []frame{{path: rootPath, depth: 0}}
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.depth > diffStreamDepthLimit {
				return fmt.Errorf("diff output depth exceeds %d", diffStreamDepthLimit)
			}
			if !f.started {
				f.started = true
				stkey := append([]byte{2}, []byte(f.path)...)
				var cur diskDiffState
				if e := json.Unmarshal(s.Get(stkey), &cur); e != nil {
					return e
				}
				if !cur.Keep && !complete && f.depth > 0 {
					stack = stack[:len(stack)-1]
					continue
				}
				cur.Node.Children = nil
				data, e := json.Marshal(diskDiffRecord{Depth: f.depth, Node: cur.Node})
				if e != nil {
					return e
				}
				if len(data) > diffStreamRecordLimit {
					return fmt.Errorf("disk diff node exceeds %d-byte record limit", diffStreamRecordLimit)
				}
				if _, e = w.Write(append(data, '\n')); e != nil {
					return e
				}
				count++
				f.cursor = tx.Bucket(diffChildrenBucket).Cursor()
				f.prefix = childIndexPrefix(2, f.path)
			}
			var k []byte
			if !f.seeked {
				k, _ = f.cursor.Seek(f.prefix)
				f.seeked = true
			} else {
				k, _ = f.cursor.Next()
			}
			if k != nil && bytes.HasPrefix(k, f.prefix) {
				child := string(k[len(f.prefix):])
				stack = append(stack, frame{path: child, depth: f.depth + 1})
				continue
			}
			stack = stack[:len(stack)-1]
		}
		if complete && count != expected {
			return fmt.Errorf("complete diff traversal reached %d of %d records; invalid parent cycle or disconnected subtree", count, expected)
		}
		return nil
	})
	return count, err
}
func childIndexPrefix(side byte, parent string) []byte {
	b := make([]byte, 5+len(parent))
	b[0] = side
	binary.BigEndian.PutUint32(b[1:5], uint32(len(parent)))
	copy(b[5:], parent)
	return b
}

var _ = mime.TypeByExtension
