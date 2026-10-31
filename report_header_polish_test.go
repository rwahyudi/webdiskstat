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

func TestParentDirectoryControlUsesFolderUpIconAndAccessibleName(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.tree-parent-btn .icon{width:17px;height:17px}`,
		`function makeUpIcon()`,
		`M3 7.5A1.5 1.5 0 0 1 4.5 6h4.2l2 2h8.8A1.5 1.5 0 0 1 21 9.5v8a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z`,
		`M12 16v-5`,
		`m9.5 13.5 2.5-2.5 2.5 2.5`,
		`button.title = parentNode ? "Go up one directory" : "Already at root";`,
		`button.setAttribute("aria-label", "Go up one directory");`,
		`button.disabled = !parentNode;`,
		`if (event.key === "Enter" || event.key === " ") event.stopPropagation();`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("generated report missing parent-directory control detail %q", want)
		}
	}
}
