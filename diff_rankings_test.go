package main

import (
	"fmt"
	"testing"
)

func TestBuildDiffReportRankingsAreBoundedAndDeterministic(t *testing.T) {
	before := &Node{Name: "/data", Path: "/data", Type: "dir"}
	after := &Node{Name: "/data", Path: "/data", Type: "dir"}
	for i := 1; i <= 120; i++ {
		before.Children = append(before.Children,
			&Node{Name: fmt.Sprintf("grow-%03d", i), Path: fmt.Sprintf("/data/grow-%03d", i), Type: "file", Size: 100},
			&Node{Name: fmt.Sprintf("shrink-%03d", i), Path: fmt.Sprintf("/data/shrink-%03d", i), Type: "file", Size: int64(i)},
			&Node{Name: fmt.Sprintf("dir-grow-%03d", i), Path: fmt.Sprintf("/data/dir-grow-%03d", i), Type: "dir", Size: 100, Children: []*Node{{Name: "leaf", Path: fmt.Sprintf("/data/dir-grow-%03d/leaf", i), Type: "file", Size: 1}}},
			&Node{Name: fmt.Sprintf("dir-shrink-%03d", i), Path: fmt.Sprintf("/data/dir-shrink-%03d", i), Type: "dir", Size: int64(i), Children: []*Node{{Name: "leaf", Path: fmt.Sprintf("/data/dir-shrink-%03d/leaf", i), Type: "file", Size: 1}}},
		)
		after.Children = append(after.Children,
			&Node{Name: fmt.Sprintf("grow-%03d", i), Path: fmt.Sprintf("/data/grow-%03d", i), Type: "file", Size: int64(100 + i)},
			&Node{Name: fmt.Sprintf("shrink-%03d", i), Path: fmt.Sprintf("/data/shrink-%03d", i), Type: "file", Size: 0},
			&Node{Name: fmt.Sprintf("dir-grow-%03d", i), Path: fmt.Sprintf("/data/dir-grow-%03d", i), Type: "dir", Size: int64(100 + i), Children: []*Node{{Name: "leaf", Path: fmt.Sprintf("/data/dir-grow-%03d/leaf", i), Type: "file", Size: 1}}},
			&Node{Name: fmt.Sprintf("dir-shrink-%03d", i), Path: fmt.Sprintf("/data/dir-shrink-%03d", i), Type: "dir", Size: 0, Children: []*Node{{Name: "leaf", Path: fmt.Sprintf("/data/dir-shrink-%03d/leaf", i), Type: "file", Size: 1}}},
		)
	}
	// Equal-magnitude rows prove that paths are the deterministic tiebreaker.
	before.Children = append(before.Children, &Node{Name: "tie-z", Path: "/data/tie-z", Type: "file", Size: 5}, &Node{Name: "tie-a", Path: "/data/tie-a", Type: "file", Size: 5})
	after.Children = append(after.Children, &Node{Name: "tie-z", Path: "/data/tie-z", Type: "file", Size: 125}, &Node{Name: "tie-a", Path: "/data/tie-a", Type: "file", Size: 125})
	// Added/removed entries with zero size are excluded; non-zero ones are ranked.
	before.Children = append(before.Children, &Node{Name: "removed", Path: "/data/removed", Type: "file", Size: 9}, &Node{Name: "removed-zero", Path: "/data/removed-zero", Type: "file"})
	after.Children = append(after.Children, &Node{Name: "added", Path: "/data/added", Type: "file", Size: 11}, &Node{Name: "added-zero", Path: "/data/added-zero", Type: "file"})

	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		rows []DiffRankingRow
		want int64
	}{
		{"growing files", report.Rankings.GrowingFiles, 120},
		{"shrinking files", report.Rankings.ShrinkingFiles, -120},
		{"growing directories", report.Rankings.GrowingDirectories, 120},
		{"shrinking directories", report.Rankings.ShrinkingDirectories, -120},
	}
	for _, check := range checks {
		if len(check.rows) != maxDiffRankingRows {
			t.Fatalf("%s rows = %d, want %d", check.name, len(check.rows), maxDiffRankingRows)
		}
		for i, row := range check.rows {
			if i > 0 && absDiffDelta(check.rows[i-1].Delta) < absDiffDelta(row.Delta) {
				t.Fatalf("%s are not descending by magnitude at %d: %#v then %#v", check.name, i, check.rows[i-1], row)
			}
			if absDiffDelta(row.Delta) < 21 {
				t.Fatalf("%s retained out-of-limit row %#v", check.name, row)
			}
		}
		if check.rows[0].Delta != check.want {
			t.Fatalf("%s top delta = %d, want %d", check.name, check.rows[0].Delta, check.want)
		}
	}
	if report.Rankings.GrowingFiles[0].Path != "/data/grow-120" || report.Rankings.GrowingFiles[len(report.Rankings.GrowingFiles)-1].Delta != 23 {
		t.Fatalf("unexpected growth ranking bounds: first=%#v last=%#v", report.Rankings.GrowingFiles[0], report.Rankings.GrowingFiles[len(report.Rankings.GrowingFiles)-1])
	}
	if report.Rankings.GrowingFiles[0].BeforeFiles != 0 || report.Rankings.GrowingFiles[0].AfterFiles != 0 {
		t.Fatalf("ranking file counts = %d/%d, want 0/0", report.Rankings.GrowingFiles[0].BeforeFiles, report.Rankings.GrowingFiles[0].AfterFiles)
	}
	if report.Increased != report.Decreased || report.Increased == 0 {
		t.Fatalf("ranking generation altered totals unexpectedly: +%d/-%d", report.Increased, report.Decreased)
	}
}

func TestBuildDiffReportRankingsExcludeZeroDeltaAndAreIndependent(t *testing.T) {
	before := &Node{Name: "/data", Path: "/data", Type: "dir", Size: 10, Children: []*Node{
		{Name: "unchanged", Path: "/data/unchanged", Type: "file", Size: 4},
		{Name: "grow", Path: "/data/grow", Type: "file", Size: 2},
		{Name: "shrink", Path: "/data/shrink", Type: "file", Size: 8},
	}}
	after := &Node{Name: "/data", Path: "/data", Type: "dir", Size: 10, Children: []*Node{
		{Name: "unchanged", Path: "/data/unchanged", Type: "file", Size: 4},
		{Name: "grow", Path: "/data/grow", Type: "file", Size: 7},
		{Name: "shrink", Path: "/data/shrink", Type: "file", Size: 3},
	}}
	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rankings.GrowingFiles) != 1 || len(report.Rankings.ShrinkingFiles) != 1 || len(report.Rankings.GrowingDirectories) != 0 || len(report.Rankings.ShrinkingDirectories) != 0 {
		t.Fatalf("unexpected rankings: %#v", report.Rankings)
	}
	if report.Rankings.GrowingFiles[0].Path != "/data/grow" || report.Rankings.GrowingFiles[0].Delta != 5 || report.Rankings.ShrinkingFiles[0].Delta != -5 {
		t.Fatalf("unexpected ranked rows: %#v", report.Rankings)
	}
	if report.Increased != 5 || report.Decreased != 5 {
		t.Fatalf("activity totals = +%d/-%d, want +5/-5", report.Increased, report.Decreased)
	}
	// Ranking rows contain scalar copies, not pointers or descendants from DiffNode.
	report.Root.Children[1].Name = "mutated"
	report.Root.Children[1].Delta = 999
	if report.Rankings.GrowingFiles[0].Name != "grow" || report.Rankings.GrowingFiles[0].Delta != 5 {
		t.Fatalf("ranking row changed with tree mutation: %#v", report.Rankings.GrowingFiles[0])
	}
}

func TestBuildDiffReportRankingsIncludeNonzeroAddedAndRemoved(t *testing.T) {
	before := &Node{Name: "/data", Path: "/data", Type: "dir", Children: []*Node{
		{Name: "removed", Path: "/data/removed", Type: "file", Size: 6},
		{Name: "removed-zero", Path: "/data/removed-zero", Type: "file"},
	}}
	after := &Node{Name: "/data", Path: "/data", Type: "dir", Children: []*Node{
		{Name: "added", Path: "/data/added", Type: "file", Size: 4},
		{Name: "added-zero", Path: "/data/added-zero", Type: "file"},
	}}
	report, err := buildDiffReport(before, after, "before", "after")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rankings.GrowingFiles) != 1 || report.Rankings.GrowingFiles[0].Path != "/data/added" || report.Rankings.GrowingFiles[0].Delta != 4 || report.Rankings.GrowingFiles[0].Status != "added" {
		t.Fatalf("added ranking = %#v", report.Rankings.GrowingFiles)
	}
	if len(report.Rankings.ShrinkingFiles) != 1 || report.Rankings.ShrinkingFiles[0].Path != "/data/removed" || report.Rankings.ShrinkingFiles[0].Delta != -6 || report.Rankings.ShrinkingFiles[0].Status != "removed" {
		t.Fatalf("removed ranking = %#v", report.Rankings.ShrinkingFiles)
	}
}
