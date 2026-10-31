package main

import (
	"encoding/json"
	"testing"
)

func TestDiffBrowserPreservesSnapshotMetadata(t *testing.T) {
	before, _ := mustDiffSnapshot(t, `[1,2,{"progname":"gdu"},[{"name":"/data","mtime":1700000000},{"name":"removed","dsize":10,"mtime":1700000001},{"name":"changed","dsize":10,"mtime":1700000002}]]`)
	after, _ := mustDiffSnapshot(t, `[1,2,{"progname":"gdu"},[{"name":"/data","mtime":1700000010},{"name":"added","dsize":10,"mtime":1700000011},{"name":"changed","dsize":20,"mtime":1700000012}]]`)
	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	if report.Root.BeforeItems != 2 || report.Root.AfterItems != 2 || report.Root.BeforeMTime != "1700000000" || report.Root.AfterMTime != "1700000010" {
		t.Fatalf("missing root metadata: %+v", report.Root)
	}
	nodes := map[string]*DiffNode{}
	for _, n := range report.Root.Children {
		nodes[n.Name] = n
	}
	if nodes["removed"].AfterMTime != "" || nodes["added"].BeforeMTime != "" || nodes["changed"].AfterMTime != "1700000012" {
		t.Fatal("incorrect metadata for missing or changed entries")
	}
	encoded, err := json.Marshal(report.Root)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DiffNode
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AfterItems != report.Root.AfterItems || decoded.AfterMTime != report.Root.AfterMTime {
		t.Fatal("browser metadata did not survive serialization")
	}
}
