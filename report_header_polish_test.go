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
