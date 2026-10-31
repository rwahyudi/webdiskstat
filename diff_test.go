package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustDiffSnapshot(t *testing.T, raw string) (*Node, diffMetadata) {
	t.Helper()
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	root, metadata, err := normalizeGDUForDiff(value)
	if err != nil {
		t.Fatal(err)
	}
	return root, metadata
}

func TestNormalizeGDUForDiffAggregatesDirectoryOwnUsage(t *testing.T) {
	root, _ := mustDiffSnapshot(t, `{"name":"/data","path":"/data","dsize":2,"children":[{"name":"file","size":10},{"name":"sub","dsize":3,"children":[{"name":"nested","size":4}]},{"name":"empty","dsize":1,"children":[]}]}`)
	if root.Size != 20 {
		t.Fatalf("root size = %d, want 20", root.Size)
	}
	if len(root.Children) != 3 {
		t.Fatalf("root children = %d, want 3", len(root.Children))
	}
	if root.Children[1].Size != 7 || root.Children[1].Type != "dir" {
		t.Fatalf("subdirectory = %#v, want size 7 dir", root.Children[1])
	}
	var empty *Node
	for _, child := range root.Children {
		if child.Name == "empty" {
			empty = child
		}
	}
	if empty == nil || empty.Type != "dir" || empty.Size != 1 {
		t.Fatalf("empty directory = %#v", empty)
	}
}

func TestBuildDiffReportChangesOnlyWithAncestorsAndZeroSizeAdded(t *testing.T) {
	before, _ := mustDiffSnapshot(t, `{"name":"/data","path":"/data","dsize":2,"children":[{"name":"same","size":5},{"name":"grow","size":4}]}`)
	after, _ := mustDiffSnapshot(t, `{"name":"/data","path":"/data","dsize":2,"children":[{"name":"same","size":5},{"name":"grow","size":9},{"name":"zero","size":0}]}`)
	report, err := buildDiffReport(before, after, "before.json", "after.json")
	if err != nil {
		t.Fatal(err)
	}
	if report.Root == nil || report.Root.Status != "grown" {
		t.Fatalf("root aggregate status = %#v", report.Root)
	}
	if len(report.Root.Children) != 2 {
		t.Fatalf("changed children = %d, want grow and zero", len(report.Root.Children))
	}
	if report.StatusCounts["grown"] != 2 || report.StatusCounts["added"] != 1 {
		t.Fatalf("status counts = %#v", report.StatusCounts)
	}
	if report.Increased != 5 || report.Decreased != 0 {
		t.Fatalf("activity totals +%d/-%d, want +5/-0", report.Increased, report.Decreased)
	}
}

func TestBuildDiffReportDirectoryOwnUsageCountedOnce(t *testing.T) {
	before, _ := mustDiffSnapshot(t, `{"name":"/data","path":"/data","dsize":2,"children":[{"name":"sub","dsize":3,"children":[{"name":"item","size":5}]}]}`)
	after, _ := mustDiffSnapshot(t, `{"name":"/data","path":"/data","dsize":4,"children":[{"name":"sub","dsize":4,"children":[{"name":"item","size":8}]}]}`)
	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	if report.Increased != 6 || report.Decreased != 0 {
		t.Fatalf("activity totals +%d/-%d, want +6/-0", report.Increased, report.Decreased)
	}
}

func TestBuildDiffReportRootCleaningAndValidation(t *testing.T) {
	before := &Node{Name: "root", Path: "/data/./", Type: "dir", Children: []*Node{}}
	after := &Node{Name: "root", Path: "/data", Type: "dir", Children: []*Node{}}
	if _, err := buildDiffReport(before, after, "before", "after"); err != nil {
		t.Fatalf("lexically equivalent roots rejected: %v", err)
	}
	after.Path = "/Data"
	if _, err := buildDiffReport(before, after, "before", "after"); err == nil || !strings.Contains(err.Error(), "roots differ") {
		t.Fatalf("case-mismatched roots error = %v", err)
	}
	after.Path = ""
	if _, err := buildDiffReport(before, after, "before", "after"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing root error = %v", err)
	}
}

func TestBuildDiffReportRejectsDuplicatePathsAndTypeReplacement(t *testing.T) {
	before := &Node{Name: "/data", Path: "/data", Type: "dir", Children: []*Node{{Name: "thing", Path: "/data/thing", Type: "file", Size: 3}}}
	after := &Node{Name: "/data", Path: "/data", Type: "dir", Children: []*Node{{Name: "thing", Path: "/data/thing", Type: "dir", Size: 3, Children: []*Node{{Name: "inside", Path: "/data/thing/inside", Type: "file", Size: 3}}}}}
	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Root.Children) != 1 || report.Root.Children[0].Status != "changed" || report.Root.Children[0].Type != "dir" {
		t.Fatalf("replacement node = %#v", report.Root.Children)
	}
	after.Children = append(after.Children, &Node{Name: "duplicate", Path: "/data/thing", Type: "file"})
	if _, err := buildDiffReport(before, after, "before", "after"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate path error = %v", err)
	}
}

func TestNormalizeGDUForDiffRejectsNcduAndMissingRoot(t *testing.T) {
	_, _, err := mustDiffSnapshotError(t, `{"prog":"ncdu","name":"/data","path":"/data"}`)
	if err == nil || !strings.Contains(err.Error(), "ncdu") {
		t.Fatalf("ncdu rejection = %v", err)
	}
	_, _, err = mustDiffSnapshotError(t, `[1,0,{"progname":"other-scanner"},[{"name":"/data","dsize":1}]]`)
	if err == nil || !strings.Contains(err.Error(), "unsupported scanner identity") {
		t.Fatalf("unsupported scanner rejection = %v", err)
	}
	_, _, err = mustDiffSnapshotError(t, `{"name":"root","size":1}`)
	if err == nil || !strings.Contains(err.Error(), "root path is unavailable") {
		t.Fatalf("missing root rejection = %v", err)
	}
}

func TestNormalizeGDUForDiffActualGDUExport(t *testing.T) {
	const export = `[1,0,{"progname":"gdu","progver":"5.30.0","timestamp":1700000000},[{"name":"/data","dsize":4096},[{"name":"empty","dsize":4096}],{"name":"a","dsize":10}]]`
	root, metadata := mustDiffSnapshot(t, export)
	if metadata.capturedAt != "1700000000" {
		t.Fatalf("captured timestamp = %q, want 1700000000", metadata.capturedAt)
	}
	if root.Size != 8202 || root.Type != "dir" {
		t.Fatalf("root size/type = %d/%s, want 8202/dir", root.Size, root.Type)
	}
	if root.Files != 1 || len(root.Children) != 2 {
		t.Fatalf("root files/children = %d/%d, want 1/2", root.Files, len(root.Children))
	}
	var empty *Node
	for _, child := range root.Children {
		if child.Name == "empty" {
			empty = child
		}
	}
	if empty == nil || empty.Type != "dir" || empty.Size != 4096 {
		t.Fatalf("single-element empty sequence = %#v, want 4096-byte dir", empty)
	}
}

func TestDiffGDU532RealisticDiskUsageExports(t *testing.T) {
	const beforeJSON = `[1,2,{"progname":"gdu","progver":"5.32.0","timestamp":1793453750},[{"name":"/tmp/opencode/webdiskstat-diff-integration/scan","mtime":1793453750},{"name":"remove.bin","asize":12288,"dsize":12288,"mtime":1793453750},{"name":"grow.bin","asize":8192,"dsize":8192,"mtime":1793453750},{"name":"same.txt","asize":9,"dsize":4096,"mtime":1793453750},[{"name":"empty","mtime":1793453750}]]]`
	const afterJSON = `[1,2,{"progname":"gdu","progver":"5.32.0","timestamp":1793453750},[{"name":"/tmp/opencode/webdiskstat-diff-integration/scan","mtime":1793453750},{"name":"grow.bin","asize":16384,"dsize":16384,"mtime":1793453750},{"name":"same.txt","asize":9,"dsize":4096,"mtime":1793453750},{"name":"new.bin","asize":4096,"dsize":4096,"mtime":1793453750},[{"name":"empty","mtime":1793453750}],{"name":"zero.bin","mtime":1793453750}]]`
	before, beforeMetadata := mustDiffSnapshot(t, beforeJSON)
	after, afterMetadata := mustDiffSnapshot(t, afterJSON)
	if beforeMetadata.capturedAt != "1793453750" || afterMetadata.capturedAt != "1793453750" {
		t.Fatalf("captured timestamps = %q/%q", beforeMetadata.capturedAt, afterMetadata.capturedAt)
	}
	if before.Size != 24576 || after.Size != 24576 || before.Files != 3 || after.Files != 4 || before.Items != 4 || after.Items != 5 {
		t.Fatalf("snapshot totals before(size=%d files=%d items=%d), after(size=%d files=%d items=%d)", before.Size, before.Files, before.Items, after.Size, after.Files, after.Items)
	}
	beforeInfo, afterInfo := snapshotInfo(before, "before.json"), snapshotInfo(after, "after.json")
	if beforeInfo.Directories != 2 || afterInfo.Directories != 2 {
		t.Fatalf("directory totals = %d/%d, want 2/2", beforeInfo.Directories, afterInfo.Directories)
	}
	report, err := buildDiffReport(before, after, "before.json", "after.json")
	if err != nil {
		t.Fatal(err)
	}
	if report.Increased != 12288 || report.Decreased != 12288 {
		t.Fatalf("gross usage change = +%d/-%d, want +12288/-12288", report.Increased, report.Decreased)
	}
	if report.Root.Before != 24576 || report.Root.After != 24576 || report.Root.BeforeFiles != 3 || report.Root.AfterFiles != 4 {
		t.Fatalf("root comparison = %#v", report.Root)
	}
	changes := map[string]*DiffNode{}
	for _, child := range report.Root.Children {
		changes[child.Name] = child
	}
	if changes["grow.bin"] == nil || changes["grow.bin"].Before != 8192 || changes["grow.bin"].After != 16384 || changes["grow.bin"].Status != "grown" {
		t.Fatalf("grow.bin change = %#v", changes["grow.bin"])
	}
	if changes["remove.bin"] == nil || changes["remove.bin"].Status != "removed" || changes["remove.bin"].Before != 12288 {
		t.Fatalf("remove.bin change = %#v", changes["remove.bin"])
	}
	if changes["new.bin"] == nil || changes["new.bin"].Status != "added" || changes["new.bin"].After != 4096 {
		t.Fatalf("new.bin change = %#v", changes["new.bin"])
	}
	if changes["zero.bin"] == nil || changes["zero.bin"].Status != "added" || changes["zero.bin"].After != 0 {
		t.Fatalf("zero.bin change = %#v", changes["zero.bin"])
	}
}

func TestBuildDiffReportActualGDUPathsAndFileCountChanges(t *testing.T) {
	before, _ := mustDiffSnapshot(t, `[1,0,{"progname":"gdu"},[{"name":"/data/","dsize":4096},[{"name":"empty","dsize":4096}]]]`)
	after, _ := mustDiffSnapshot(t, `[1,0,{"progname":"gdu"},[{"name":"/data","dsize":4096},[{"name":"empty","dsize":4096}],{"name":"zero","dsize":0}]]`)
	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	if report.Root.Path != "/data" || report.Root.Status != "changed" {
		t.Fatalf("trailing slash root = %#v", report.Root)
	}
	if report.Before.Files != 0 || report.After.Files != 1 || report.StatusCounts["added"] != 1 || report.StatusCounts["changed"] == 0 {
		t.Fatalf("zero-size file count change not represented: snapshots=%#v/%#v statuses=%#v", report.Before, report.After, report.StatusCounts)
	}
}

func TestNormalizeGDUForDiffWrappedMappingAggregatesDirectories(t *testing.T) {
	root, metadata, err := mustDiffSnapshotError(t, `{"metadata":{"progname":"gdu","timestamp":1700000000},"root":{"name":"/data/","dsize":4096,"children":{"file":{"name":"file","size":10},"empty":{"name":"empty","dsize":4096,"children":[]}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.capturedAt != "1700000000" {
		t.Fatalf("wrapped captured timestamp = %q", metadata.capturedAt)
	}
	if root.Size != 8202 {
		t.Fatalf("wrapped root size = %d, want 8202", root.Size)
	}
	if root.Path != "/data/" {
		t.Fatalf("wrapped root path = %q", root.Path)
	}
	for _, child := range root.Children {
		if child.Name == "empty" && child.Type != "dir" {
			t.Fatalf("wrapped empty directory type = %q", child.Type)
		}
	}
	_, _, err = mustDiffSnapshotError(t, `{"metadata":{"progname":"ncdu"},"root":{"name":"/data","dsize":1}}`)
	if err == nil || !strings.Contains(err.Error(), "ncdu") {
		t.Fatalf("wrapped ncdu metadata error = %v", err)
	}
}

func mustDiffSnapshotError(t *testing.T, raw string) (*Node, diffMetadata, error) {
	t.Helper()
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return normalizeGDUForDiff(value)
}
