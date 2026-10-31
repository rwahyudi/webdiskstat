package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffReportSharedAppearance(t *testing.T) {
	html, err := renderDiffReport(diffReportFixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	css, control, err := diffReportAppearance()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, css) || !strings.Contains(html, control) {
		t.Fatal("comparison must reuse optimized normal palette and switcher")
	}
	initial := strings.Index(html, "localStorage.getItem('webdiskstat-theme')")
	if initial < 0 || initial > strings.Index(html, "<style>") || initial > strings.Index(html, "<body>") {
		t.Fatal("theme restoration must precede first styled paint")
	}
	for _, required := range []string{"color-scheme:dark", "color-scheme:light", "theme-switcher-v2", "aria-checked", "prefers-reduced-motion", "localStorage.setItem(THEME_STORAGE_KEY, normalized)"} {
		if !strings.Contains(html, required) {
			t.Errorf("missing theme feature %s", required)
		}
	}
}

func TestDiffReportThemeBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; theme browser verification skipped")
	}
	dir := t.TempDir()
	password := "theme-password"
	for _, fixture := range []struct {
		name     string
		password *string
	}{{"plain", nil}, {"encrypted", &password}} {
		html, err := renderDiffReport(diffReportFixture(), fixture.password)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.name+".html"), []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root := &Node{Name: "private-root", Path: "/private-root", Type: "dir"}
	normal, err := renderReport(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "normal.html"), []byte(normal), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(diffReportThemeBrowserCheck)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("theme browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const diffReportThemeBrowserCheck = diffBrowserThemeCheck
