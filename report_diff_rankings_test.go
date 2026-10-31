package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func diffRankingsFixture() *DiffReport {
	report := diffReportFixture()
	sets := []struct {
		key    string
		kind   string
		growth bool
		rows   *[]DiffRankingRow
	}{
		{"growingFiles", "file", true, &report.Rankings.GrowingFiles},
		{"shrinkingFiles", "file", false, &report.Rankings.ShrinkingFiles},
		{"growingDirectories", "dir", true, &report.Rankings.GrowingDirectories},
		{"shrinkingDirectories", "dir", false, &report.Rankings.ShrinkingDirectories},
	}
	for _, set := range sets {
		*set.rows = nil
		for i := 0; i < 100; i++ {
			name := fmt.Sprintf("%s-%03d", set.key, i)
			delta := int64(100-i) * 1024 * 1024
			before, after, status := int64(0), delta, "added"
			if !set.growth {
				before, after, delta, status = delta, 0, -delta, "removed"
			}
			*set.rows = append(*set.rows, DiffRankingRow{Name: name, Path: "/ranking-only/" + name, Type: set.kind, Before: before, After: after, Delta: delta, Status: status})
		}
	}
	// These flat records resolve into actual browser objects only when clicked.
	// They deliberately precede larger values to catch any client-side sorting.
	report.Rankings.GrowingFiles[0] = DiffRankingRow{Name: "grow.bin", Path: "/private-root/offset/grow.bin", Type: "file", After: 100, Delta: 100, Status: "added"}
	report.Rankings.GrowingDirectories[0] = DiffRankingRow{Name: "offset", Path: "/private-root/offset", Type: "dir", Before: 100, After: 100, Status: "unchanged"}
	return report
}

func TestDiffReportRankingsBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; rankings browser verification skipped")
	}
	dir := t.TempDir()
	password := "rankings-password"
	empty := diffReportFixture()
	empty.Rankings = DiffRankings{}
	for _, fixture := range []struct {
		name     string
		report   *DiffReport
		password *string
	}{{"plain", diffRankingsFixture(), nil}, {"encrypted", diffRankingsFixture(), &password}, {"empty", empty, nil}} {
		html, err := renderDiffReport(fixture.report, fixture.password)
		if err != nil {
			t.Fatal(err)
		}
		if fixture.password != nil && (strings.Contains(html, "/ranking-only/") || strings.Contains(html, "growingFiles-001")) {
			t.Fatal("encrypted artifact exposes ranking dataset")
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.name+".html"), []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(diffRankingsBrowserCheck)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("rankings browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const diffRankingsBrowserCheck = diffBrowserRankingsCheck
