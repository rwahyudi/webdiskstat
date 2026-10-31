package main

import (
	"strings"
	"testing"
)

func TestTreeHeaderSeparatorsHaveContentBreathingRoom(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const headerSpacing = `.tree-header-cell{min-width:0;position:relative;display:flex;align-items:center;padding-inline-end:10px}`
	if !strings.Contains(report, headerSpacing) {
		t.Fatalf("generated report missing trailing space before tree column separators: %s", headerSpacing)
	}
}

func TestTreeHeaderOmitsSelectedFolderOpenAction(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"tree-open-btn", "makeSelectedFolderButton", "syncSelectedFolderButton"} {
		if strings.Contains(report, removed) {
			t.Errorf("generated report still contains removed tree-header action %q", removed)
		}
	}
}
