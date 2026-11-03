package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiskDiffNativeMatchesLegacy(t *testing.T) {
	beforeJSON := `[1,2,{"progname":"gdu","timestamp":1700000000},[{"name":"/data","dsize":4},[{"name":"empty","dsize":2}],{"name":"a","dsize":10},{"name":"a-foo","dsize":3}]]`
	afterJSON := `[1,2,{"progname":"gdu","timestamp":1700000010},[{"name":"/data","dsize":4},{"name":"a-foo","dsize":5},{"name":"a","dsize":12},[{"name":"empty","dsize":2}]]]`
	before, _ := mustDiffSnapshot(t, beforeJSON)
	after, _ := mustDiffSnapshot(t, afterJSON)
	legacy, err := buildDiffReportWithOptions(before, after, "before", "after", diffOptions{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bp := filepath.Join(dir, "before.json")
	ap := filepath.Join(dir, "after.json")
	if err = os.WriteFile(bp, []byte(beforeJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(ap, []byte(afterJSON), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := buildDiskDiffReport(bp, ap, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	var records []diskDiffRecord
	f, err := os.Open(got.RecordsPath)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(f)
	for {
		var rec diskDiffRecord
		err := dec.Decode(&rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	_ = f.Close()
	if got.Before.Root != legacy.Before.Root || got.After.Root != legacy.After.Root || got.Before.Size != legacy.Before.Size || got.After.Size != legacy.After.Size {
		t.Fatalf("snapshots differ: got %#v %#v legacy %#v %#v", got.Before, got.After, legacy.Before, legacy.After)
	}
	if len(records) != 4 {
		t.Fatalf("record count=%d, want 4; records=%+v", len(records), records)
	}
	if got.Increased != legacy.Increased || got.Decreased != legacy.Decreased {
		t.Fatalf("allocation totals=(%d,%d), legacy=(%d,%d)", got.Increased, got.Decreased, legacy.Increased, legacy.Decreased)
	}
}

func TestDiskDiffLexicalLimits(t *testing.T) {
	input := `{"value":"` + strings.Repeat("x", diffStreamStringLimit+1) + `"}`
	if err := validateDiffLexical(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "string exceeds") {
		t.Fatalf("oversized JSON string error = %v", err)
	}
	number := `{"value":` + strings.Repeat("9", diffStreamNumberLimit+1) + `}`
	if err := validateDiffLexical(strings.NewReader(number)); err == nil || !strings.Contains(err.Error(), "number exceeds") {
		t.Fatalf("oversized JSON number error = %v", err)
	}
}

func TestDiskDiffUnchangedDefaultKeepsRoot(t *testing.T) {
	input := `[1,2,{"progname":"gdu"},[{"name":"/same","dsize":0}]]`
	dir := t.TempDir()
	before := filepath.Join(dir, "before.json")
	after := filepath.Join(dir, "after.json")
	if err := os.WriteFile(before, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := buildDiskDiffReport(before, after, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	if report.Count != 1 {
		t.Fatalf("default unchanged record count=%d, want root only", report.Count)
	}
	var buf bytes.Buffer
	if err := writeDiskDiffJSON(&buf, report); err != nil {
		t.Fatal(err)
	}
	var decoded DiffReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Root == nil || decoded.Root.Path != "/same" || decoded.Root.Status != "unchanged" {
		t.Fatalf("unchanged default root = %#v", decoded.Root)
	}
}

func TestDiskDiffEmptyDirectoryItemsDoNotChangeParentStatus(t *testing.T) {
	beforeJSON := `[1,2,{"progname":"gdu"},[{"name":"/r"}]]`
	afterJSON := `[1,2,{"progname":"gdu"},[{"name":"/r"},[{"name":"empty"}]]]`
	before, _ := mustDiffSnapshot(t, beforeJSON)
	after, _ := mustDiffSnapshot(t, afterJSON)
	legacy, err := buildDiffReportWithOptions(before, after, "b", "a", diffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bp, ap := filepath.Join(dir, "b.json"), filepath.Join(dir, "a.json")
	_ = os.WriteFile(bp, []byte(beforeJSON), 0600)
	_ = os.WriteFile(ap, []byte(afterJSON), 0600)
	report, err := buildDiskDiffReport(bp, ap, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	records := readDiskDiffRecords(t, report.RecordsPath)
	got := map[string]string{}
	for _, r := range records {
		got[r.Node.Path] = r.Node.Status
	}
	if legacy.Root.Status != "unchanged" || got["/r"] != "unchanged" || got["/r/empty"] != "added" {
		t.Fatalf("legacy root=%q streamed statuses=%v", legacy.Root.Status, got)
	}
}

func TestDiskDiffNativeObjectDirectoryNormalization(t *testing.T) {
	beforeJSON := `[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"folder","isDir":true,"size":0,"mime":"text/plain"}]]`
	afterJSON := `[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"folder","isDir":true,"size":0}]]`
	before, _ := mustDiffSnapshot(t, beforeJSON)
	after, _ := mustDiffSnapshot(t, afterJSON)
	legacy, err := buildDiffReportWithOptions(before, after, "b", "a", diffOptions{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bp, ap := filepath.Join(dir, "b.json"), filepath.Join(dir, "a.json")
	_ = os.WriteFile(bp, []byte(beforeJSON), 0600)
	_ = os.WriteFile(ap, []byte(afterJSON), 0600)
	report, err := buildDiskDiffReport(bp, ap, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	records := readDiskDiffRecords(t, report.RecordsPath)
	got := map[string]DiffNode{}
	for _, r := range records {
		got[r.Node.Path] = r.Node
	}
	want := legacy.Root.Children[0]
	node := got["/r/folder"]
	if node.Type != "dir" || node.Status != want.Status || node.BeforeFiles != 0 || node.AfterFiles != 0 {
		t.Fatalf("native object directory=%+v, legacy=%+v", node, want)
	}
}

func readDiskDiffRecords(t *testing.T, path string) []diskDiffRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var rows []diskDiffRecord
	for {
		var row diskDiffRecord
		err := dec.Decode(&row)
		if err == io.EOF {
			return rows
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
}

func TestDiskDiffCompatibilityWrapperAndCleanup(t *testing.T) {
	before := `{"metadata":{"progname":"gdu","root":"/r"},"tree":{"name":"/r","children":{"x":{"size":2}}}}`
	after := `{"tree":{"children":{"x":{"size":3}},"name":"/r"},"metadata":{"root":"/r","progname":"gdu"}}`
	dir := t.TempDir()
	b := filepath.Join(dir, "b.json")
	a := filepath.Join(dir, "a.json")
	if err := os.WriteFile(b, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := buildDiskDiffReport(b, a, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	base := report.dir
	if info, err := os.Stat(base); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private temp dir: %v %v", info, err)
	}
	if err := report.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("temporary state still exists: %v", err)
	}
	if err := report.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDiffCompatibilityPreflightBoundsExpandedPaths(t *testing.T) {
	root := map[string]any{"name": "/"}
	children := make([]any, 100)
	for i := range children {
		children[i] = map[string]any{"name": "child"}
	}
	root["children"] = children
	if err := preflightDiffNormalization(root, 128, 1000); err == nil || !strings.Contains(err.Error(), "normalized paths") {
		t.Fatalf("preflight error = %v, want path expansion limit", err)
	}
}

func TestDiffCompatibilityPreflightRejectsLongInheritedPaths(t *testing.T) {
	for _, kind := range []string{"numeric", "object"} {
		t.Run(kind, func(t *testing.T) {
			children := make([]any, 10_000)
			for i := range children {
				if kind == "numeric" {
					children[i] = json.Number("1")
				} else {
					children[i] = map[string]any{"name": "x"}
				}
			}
			raw := map[string]any{"name": strings.Repeat("r", 8<<10), "children": children}
			// Deliberately do not normalize: preflight must reject the expansion
			// before any of those inherited full-path strings are constructed.
			if err := preflightDiffNormalization(raw, 64<<20, maxReportNodes); err == nil || !strings.Contains(err.Error(), "normalized paths") {
				t.Fatalf("long inherited paths were not rejected: %v", err)
			}
		})
	}
}

func TestDiffNativeActiveMetadataBudget(t *testing.T) {
	if err := checkNativeActiveMetadata(8, 3, 10); err == nil {
		t.Fatal("expected aggregate active metadata budget failure")
	}
	if err := checkNativeActiveMetadata(8, 2, 10); err != nil {
		t.Fatalf("metadata at exact budget: %v", err)
	}
}

func TestDiskDiffCompatibilityFallbackMatchesLegacy(t *testing.T) {
	fixtures := []struct {
		before, after string
	}{
		{`[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"d","children":[]}]]`, `[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"d","children":[{"name":"x","size":2}]}]]`},
		{`{"metadata":{"progname":"gdu","root":"/r"},"tree":{"name":"/r","children":{"x":{"size":2}}}}`, `{"metadata":{"progname":"gdu","root":"/r"},"tree":{"name":"/r","children":{"x":{"size":3}}}}`},
		{`[1,2,{"progname":"gdu","root":"/r"},[1,2]]`, `[1,2,{"progname":"gdu","root":"/r"},[1,3]]`},
	}
	for i, fixture := range fixtures {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			assertDiskMatchesLegacy(t, fixture.before, fixture.after)
		})
	}
}

func TestDiskDiffNativeDirectoryMIMEParity(t *testing.T) {
	assertDiskMatchesLegacy(t,
		`[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"folder","size":0,"mime":"text/plain"}]]`,
		`[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"folder","size":0}]]`)
}

func assertDiskMatchesLegacy(t *testing.T, beforeJSON, afterJSON string) {
	t.Helper()
	before, _ := mustDiffSnapshot(t, beforeJSON)
	after, _ := mustDiffSnapshot(t, afterJSON)
	legacy, err := buildDiffReportWithOptions(before, after, "before", "after", diffOptions{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bp, ap := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	if err = os.WriteFile(bp, []byte(beforeJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(ap, []byte(afterJSON), 0600); err != nil {
		t.Fatal(err)
	}
	disk, err := buildDiskDiffReport(bp, ap, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	var streamed DiffReport
	var buf bytes.Buffer
	if err = writeDiskDiffJSON(&buf, disk); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(buf.Bytes(), &streamed); err != nil {
		t.Fatal(err)
	}
	legacy.Before.Source, legacy.After.Source = "", ""
	streamed.Before.Source, streamed.After.Source = "", ""
	if !reflect.DeepEqual(legacy, &streamed) {
		t.Fatalf("legacy report differs from streamed report\nlegacy:  %#v\nstreamed: %#v", legacy, streamed)
	}
}
