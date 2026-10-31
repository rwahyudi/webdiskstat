package main

import (
	"regexp"
	"strings"
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

func TestSortableTreeHeaderCellsSortAcrossWhitespace(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.tree-header-cell.sortable{cursor:pointer}.tree-header-cell.sortable:hover{background:var(--row-hover)}`,
		`cell.setAttribute("role", "columnheader");`,
		`cell.setAttribute("aria-sort", state.sortKey === column.sortKey`,
		`cell.addEventListener("click", event => {`,
		`if (event.target.closest("button, input, select, a")) return;`,
		`setSort(column.sortKey);`,
		`button.setAttribute("aria-label", state.sortKey === key`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("generated report missing full-area sortable header behavior %q", want)
		}
	}
}
