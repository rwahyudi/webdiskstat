package main

import (
	"strings"
	"testing"
)

func TestCopiedPathsKeepTheirTailAndExposeFullTextTooltip(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.path-copy-text{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;direction:rtl;text-align:left}`,
		`pathText.title = filePath;`,
		`const pathValue = document.createElement("bdi");`,
		`pathValue.dir = "ltr";`,
		`pathValue.textContent = filePath;`,
		`pathLine.className = "path-copy-line";`,
		`pathLine.append(pathText, makeCopyPathButton(filePath, "Copy file path"));`,
		`text.title = path;`,
		`pathValue.textContent = path;`,
		`copyPathValue(path, button);`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("generated report missing path tail/tooltip behavior %q", want)
		}
	}
}

func TestRootBreadcrumbIconPrecedesLocationText(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	navIndex := strings.Index(report, `<nav id="crumbs"`)
	locationIndex := strings.Index(report, `id="currentLocation"`)
	if navIndex < 0 || (locationIndex >= 0 && navIndex >= locationIndex) {
		t.Fatal("root breadcrumb navigation and icon must appear before the current-location text")
	}
	for _, want := range []string{
		`.crumb.root .crumb-home-icon{width:18px;height:18px;stroke-width:1.8}`,
		`svg.setAttribute("class", "icon crumb-home-icon");`,
		`svg.setAttribute("viewBox", "0 0 24 24");`,
		`svg.setAttribute("aria-hidden", "true");`,
		`M3 10.5 12 3l9 7.5`,
		`if (index === 0) {`,
		`button.appendChild(makeHomeIcon());`,
		`button.setAttribute("aria-label", index === 0 ? "Root" : node.name);`,
		`rootButton.appendChild(makeHomeIcon());`,
		`rootButton.setAttribute("aria-label", "Root");`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("generated report missing root breadcrumb icon detail %q", want)
		}
	}
}
