package main

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

type DiffReport struct {
	Before       DiffSnapshot   `json:"before"`
	After        DiffSnapshot   `json:"after"`
	Root         *DiffNode      `json:"root"`
	Increased    int64          `json:"increased"`
	Decreased    int64          `json:"decreased"`
	StatusCounts map[string]int `json:"statusCounts"`
	Rankings     DiffRankings   `json:"rankings"`
}

type DiffSnapshot struct {
	Source      string `json:"source"`
	Root        string `json:"root"`
	Size        int64  `json:"size"`
	Files       int    `json:"files"`
	Directories int    `json:"directories"`
	CapturedAt  string `json:"capturedAt,omitempty"`
}

type DiffNode struct {
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	Type        string      `json:"type"`
	BeforeType  string      `json:"beforeType,omitempty"`
	AfterType   string      `json:"afterType,omitempty"`
	Before      int64       `json:"before"`
	After       int64       `json:"after"`
	Delta       int64       `json:"delta"`
	BeforeFiles int         `json:"beforeFiles"`
	AfterFiles  int         `json:"afterFiles"`
	BeforeItems int         `json:"beforeItems"`
	AfterItems  int         `json:"afterItems"`
	BeforeMTime string      `json:"beforeMtime,omitempty"`
	AfterMTime  string      `json:"afterMtime,omitempty"`
	Status      string      `json:"status"`
	Children    []*DiffNode `json:"children,omitempty"`
}

type diffMetadata struct {
	root, capturedAt string
}

func buildDiffReport(before, after *Node, beforeSource, afterSource string) (*DiffReport, error) {
	if before == nil || after == nil {
		return nil, fmt.Errorf("both snapshots must have a root")
	}
	beforeRoot, afterRoot := cleanDiffRoot(before.Path), cleanDiffRoot(after.Path)
	if beforeRoot == "" || afterRoot == "" {
		return nil, fmt.Errorf("snapshot root is unavailable")
	}
	if beforeRoot != afterRoot {
		return nil, fmt.Errorf("snapshot roots differ: baseline %q, current %q", beforeRoot, afterRoot)
	}
	bIdx, err := indexDiffTree(before, beforeRoot, "baseline")
	if err != nil {
		return nil, err
	}
	aIdx, err := indexDiffTree(after, afterRoot, "current")
	if err != nil {
		return nil, err
	}
	if len(bIdx)+len(aIdx) > maxReportNodes {
		return nil, fmt.Errorf("comparison contains more than %d combined nodes", maxReportNodes)
	}
	statuses := map[string]int{"added": 0, "removed": 0, "grown": 0, "shrunk": 0, "changed": 0, "unchanged": 0}
	increased, decreased := int64(0), int64(0)
	rankings := newDiffRankingsBuilder()
	var build func(string) *DiffNode
	build = func(p string) *DiffNode {
		b, bok := bIdx[p]
		a, aok := aIdx[p]
		var node *DiffNode
		name := path.Base(p)
		if p == beforeRoot {
			name = before.Name
		}
		if bok && b.Name != "" {
			name = b.Name
		}
		if aok && a.Name != "" {
			name = a.Name
		}
		bt, at := "", ""
		if bok {
			bt = b.Type
		}
		if aok {
			at = a.Type
		}
		typ := at
		if typ == "" {
			typ = bt
		}
		if bt == "dir" || at == "dir" {
			typ = "dir"
		}
		bv, av, bf, af := int64(0), int64(0), 0, 0
		if bok {
			bv, bf = b.Size, b.Files
		}
		if aok {
			av, af = a.Size, a.Files
		}
		status := "unchanged"
		switch {
		case !bok:
			status = "added"
		case !aok:
			status = "removed"
		case bt != at:
			status = "changed"
		case av > bv:
			status = "grown"
		case av < bv:
			status = "shrunk"
		case bf != af || b.MTime != a.MTime || b.Flag != a.Flag || b.MIME != a.MIME:
			status = "changed"
		}
		delta := av - bv
		rankings.add(DiffRankingRow{Name: name, Path: p, Type: typ, Before: bv, After: av, Delta: delta, BeforeFiles: bf, AfterFiles: af, Status: status})
		if status != "unchanged" {
			statuses[status]++
		}
		// Directory totals include descendants. Count only a directory's own
		// allocation plus files, so an activity's net is never counted twice.
		countedDelta := delta
		if (bok && bt == "dir") || (aok && at == "dir") {
			beforeChildren, afterChildren := int64(0), int64(0)
			if bok {
				for _, child := range b.Children {
					beforeChildren += child.Size
				}
			}
			if aok {
				for _, child := range a.Children {
					afterChildren += child.Size
				}
			}
			countedDelta = (av - afterChildren) - (bv - beforeChildren)
		}
		if countedDelta > 0 {
			increased += countedDelta
		}
		if countedDelta < 0 {
			decreased -= countedDelta
		}
		node = &DiffNode{Name: name, Path: p, Type: typ, BeforeType: bt, AfterType: at, Before: bv, After: av, Delta: delta, BeforeFiles: bf, AfterFiles: af, Status: status}
		if bok {
			node.BeforeItems, node.BeforeMTime = b.Items, b.MTime
		}
		if aok {
			node.AfterItems, node.AfterMTime = a.Items, a.MTime
		}
		children := map[string]bool{}
		if bok {
			for _, c := range b.Children {
				children[cleanDiffRoot(c.Path)] = true
			}
		}
		if aok {
			for _, c := range a.Children {
				children[cleanDiffRoot(c.Path)] = true
			}
		}
		keys := make([]string, 0, len(children))
		for k := range children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := build(k)
			if child.Status != "unchanged" || len(child.Children) > 0 {
				node.Children = append(node.Children, child)
			}
		}
		return node
	}
	root := build(beforeRoot)
	return &DiffReport{Before: snapshotInfo(before, beforeSource), After: snapshotInfo(after, afterSource), Root: root, Increased: increased, Decreased: decreased, StatusCounts: statuses, Rankings: rankings.report()}, nil
}

func cleanDiffRoot(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return path.Clean(value)
}

func indexDiffTree(root *Node, rootPath, label string) (map[string]*Node, error) {
	result := make(map[string]*Node)
	stack := []*Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		p := cleanDiffRoot(n.Path)
		if p == "" {
			return nil, fmt.Errorf("%s snapshot contains a node with unavailable path", label)
		}
		if _, ok := result[p]; ok {
			return nil, fmt.Errorf("%s snapshot has duplicate or ambiguous path %q", label, p)
		}
		result[p] = n
		for _, c := range n.Children {
			if c == nil {
				return nil, fmt.Errorf("%s snapshot contains a null node", label)
			}
			stack = append(stack, c)
		}
	}
	if result[rootPath] != root {
		return nil, fmt.Errorf("%s snapshot root path is inconsistent", label)
	}
	return result, nil
}

func snapshotInfo(root *Node, source string) DiffSnapshot {
	files, dirs := 0, 0
	stack := []*Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type == "dir" {
			dirs++
		} else {
			files++
		}
		stack = append(stack, n.Children...)
	}
	return DiffSnapshot{Source: source, Root: cleanDiffRoot(root.Path), Size: root.Size, Files: files, Directories: dirs}
}

// normalizeGDUForDiff corrects gdu's directory size semantics: dsize is the
// directory's own allocation, so child allocations are added exactly once.
func normalizeGDUForDiff(raw any) (*Node, diffMetadata, error) {
	meta := diffMetadata{}
	tree := raw
	metadata := map[string]any(nil)
	if arr, ok := raw.([]any); ok && isExportArray(arr) {
		metadata = arr[2].(map[string]any)
		if scanner := scannerMetadataIdentity(metadata); scanner != "" && !strings.EqualFold(scanner, "gdu") {
			return nil, meta, scannerIdentityError(scanner)
		}
		tree = arr[3]
	} else if mapping, ok := raw.(map[string]any); ok {
		metadata = wrappedGDUResourceMetadata(mapping)
		if scanner := scannerMetadataIdentity(metadata); scanner != "" && !strings.EqualFold(scanner, "gdu") {
			return nil, meta, scannerIdentityError(scanner)
		}
		// Match normalizeExport's wrapper unwrapping so the size correction
		// walks the same tree that produced the normalized Nodes.
		tree = unwrapPossibleRoot(mapping)
	}
	meta.root = metadataValue(metadata, "root", "Root", "path", "Path", "root_path", "rootPath")
	meta.capturedAt = metadataValue(metadata, "timestamp", "Timestamp", "capturedAt", "captured_at", "scan_time", "scanTime")
	root, err := normalizeExport(raw, "gdu")
	if err != nil {
		return nil, meta, err
	}
	if root.Path == "root" && meta.root != "" {
		root.Path = meta.root
	}
	if root.Path == "root" && meta.root == "" {
		return nil, meta, fmt.Errorf("gdu snapshot root path is unavailable")
	}
	own := make(map[string]int64)
	collectGDUOwnSizes(tree, "", own)
	type frame struct {
		node    *Node
		visited bool
	}
	stack := []frame{{node: root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !f.visited {
			stack = append(stack, frame{node: f.node, visited: true})
			for _, c := range f.node.Children {
				stack = append(stack, frame{node: c})
			}
			continue
		}
		n := f.node
		ownSize, isDirectory := own[n.Path]
		if isDirectory {
			n.Type = "dir"
		}
		if n.Type == "dir" {
			if isDirectory {
				n.Size = ownSize
			} else if len(n.Children) > 0 {
				n.Size = 0
			}
			for _, c := range n.Children {
				n.Size += c.Size
			}
		}
	}
	if err := addTotals(root); err != nil {
		return nil, meta, err
	}
	if meta.root == "" {
		meta.root = root.Path
	}
	if cleanDiffRoot(meta.root) != cleanDiffRoot(root.Path) {
		return nil, meta, fmt.Errorf("gdu metadata root %q does not match tree root %q", meta.root, root.Path)
	}
	return root, meta, nil
}

func wrappedGDUResourceMetadata(raw map[string]any) map[string]any {
	for _, key := range []string{"metadata", "Metadata", "meta", "Meta"} {
		if metadata, ok := raw[key].(map[string]any); ok {
			return metadata
		}
	}
	return raw
}

func scannerMetadataIdentity(m map[string]any) string {
	for key, value := range m {
		lk := strings.ToLower(key)
		if lk == "prog" || lk == "progname" || lk == "program" || lk == "programname" {
			if name, ok := value.(string); ok {
				return strings.TrimSpace(name)
			}
			return fmt.Sprint(value)
		}
	}
	return ""
}
func scannerIdentityError(scanner string) error {
	if strings.EqualFold(scanner, "ncdu") {
		return fmt.Errorf("expected gdu JSON; input metadata identifies an ncdu export")
	}
	return fmt.Errorf("expected gdu JSON; unsupported scanner identity %q", scanner)
}
func metadataValue(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			if n, ok := v.(json.Number); ok {
				return n.String()
			}
		}
	}
	return ""
}

func collectGDUOwnSizes(raw any, parent string, out map[string]int64) {
	var info map[string]any
	var children []any
	var childNames []string
	sequenceNode := false
	switch v := raw.(type) {
	case map[string]any:
		info = v
		if a, ok := extractChildren(v); ok {
			switch items := a.(type) {
			case []any:
				children = items
			case map[string]any:
				childNames = make([]string, 0, len(items))
				for name := range items {
					childNames = append(childNames, name)
				}
				sort.Strings(childNames)
				for _, name := range childNames {
					children = append(children, items[name])
				}
			}
		}
	case []any:
		if isSequenceNode(v) {
			sequenceNode = true
			info, _ = v[0].(map[string]any)
			children = v[1:]
		} else {
			for _, item := range v {
				collectGDUOwnSizes(item, parent, out)
			}
			return
		}
	}
	if info == nil {
		return
	}
	name := firstString(info, nameKeys)
	p := firstString(info, pathKeys)
	if name == "" {
		name = displayNameFromPath(p)
	}
	if name == "" {
		name = "root"
	}
	if p == "" {
		p = makePath(parent, name)
	} else if parent != "" && p == name {
		p = makePath(parent, name)
	}
	if sequenceNode || firstBool(info, dirKeys) || len(children) > 0 || hasEmptyDirectoryChildren(info) {
		out[p] = firstNumber(info, sizeKeys)
	}
	for i, c := range children {
		fallback := fmt.Sprintf("item-%d", i)
		if i < len(childNames) {
			fallback = childNames[i]
		}
		collectGDUOwnSizesWithFallback(c, p, out, fallback)
	}
}
func collectGDUOwnSizesWithFallback(raw any, parent string, out map[string]int64, fallback string) {
	if m, ok := raw.(map[string]any); ok && firstString(m, nameKeys) == "" && firstString(m, pathKeys) == "" {
		cp := map[string]any{}
		for k, v := range m {
			cp[k] = v
		}
		cp["name"] = fallback
		collectGDUOwnSizes(cp, parent, out)
		return
	}
	if a, ok := raw.([]any); ok && !isSequenceNode(a) {
		collectGDUOwnSizes(raw, parent, out)
		return
	}
	collectGDUOwnSizes(raw, parent, out)
}
func hasEmptyDirectoryChildren(info map[string]any) bool {
	for _, k := range childKeys {
		if v, ok := info[k]; ok {
			if a, ok := v.([]any); ok && len(a) == 0 {
				return true
			}
			if m, ok := v.(map[string]any); ok && len(m) == 0 {
				return true
			}
		}
	}
	return false
}
