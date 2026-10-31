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
