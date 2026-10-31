package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestTreemapTileLabelsAndBadgesUseRegularWeight(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, selector := range []string{
		`.tile-label{`,
		`.tile.dir .tile-label{`,
		`.tile-kind{`,
	} {
		pattern := `(?:^|\})` + regexp.QuoteMeta(selector) + `([^}]*)\}`
		match := regexp.MustCompile(pattern).FindStringSubmatch(report)
		if len(match) != 2 {
			t.Fatalf("generated report is missing treemap rule %q", selector)
		}
		rule := match[1]
		if !strings.Contains(rule, "font-weight:400") {
			t.Errorf("treemap rule %q must explicitly use regular font weight: %s", selector, rule)
		}
	}
}
