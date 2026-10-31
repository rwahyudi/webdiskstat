package main

import (
	"regexp"
	"testing"
)

func TestReportWorkspacePanelsUseTighterCorners(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{".main", ".treemap-frame", ".top-files"} {
		pattern := regexp.QuoteMeta(selector) + `\{[^}]*border-radius:4px(?:;|\})`
		if !regexp.MustCompile(pattern).MatchString(report) {
			t.Errorf("generated report missing tighter workspace panel corners for %s", selector)
		}
	}
}
