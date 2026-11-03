package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCLIProgressSummaryAndSanitization(t *testing.T) {
	var out bytes.Buffer
	p := &cliProgress{writer: &out, started: time.Now(), active: true, width: 80}
	if !p.Finish(cliSummary{Path: "/tmp/report\x1b[31m.html", Bytes: 1536, Elapsed: 12 * time.Millisecond, Files: 1234, Dirs: 12, Diff: map[string]int{"added": 1, "removed": 2, "grown": 3, "shrunk": 4, "changed": 5}}) {
		t.Fatal("expected interactive summary")
	}
	got := out.String()
	for _, want := range []string{"Report ready", "/tmp/report[31m.html", "1.5 KB", "1,234 files", "+1 added", "−2 removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[31m") {
		t.Fatal("terminal escape passed through filename")
	}
}

func TestCLIProgressDisabledForNonTerminalWriter(t *testing.T) {
	var out bytes.Buffer
	p := newCLIProgress(&out, nil, time.Now())
	p.Update("Reading")
	if p.Finish(cliSummary{}) {
		t.Fatal("non-terminal should not get pretty output")
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestCLIProgressActiveSpinnerLifecycle(t *testing.T) {
	var out bytes.Buffer
	p := &cliProgress{writer: &out, started: time.Now(), active: true, color: false, width: 40}
	p.Update("Reading scan data")
	time.Sleep(20 * time.Millisecond)
	if !p.Finish(cliSummary{Path: "/tmp/report.html", Bytes: 25, Elapsed: 20 * time.Millisecond, Files: 2, Dirs: 1}) {
		t.Fatal("expected interactive summary")
	}
	if !p.Finish(cliSummary{}) {
		t.Fatal("finish should be idempotent")
	}
	time.Sleep(120 * time.Millisecond)
	got := out.String()
	if !strings.Contains(got, "Reading scan data") || !strings.Contains(got, "Report ready") {
		t.Fatalf("spinner lifecycle output incomplete: %q", got)
	}
	if strings.Count(got, "Report ready") != 1 {
		t.Fatalf("summary printed more than once: %q", got)
	}
}

func TestCLIProgressCloseBeforeErrorOutput(t *testing.T) {
	var out bytes.Buffer
	p := &cliProgress{writer: &out, started: time.Now(), active: true, width: 40}
	p.Update("Reading input")
	time.Sleep(10 * time.Millisecond)
	p.Close()
	if _, err := out.WriteString("webdiskstat: expected failure\n"); err != nil {
		t.Fatal(err)
	}
	p.Close()
	time.Sleep(120 * time.Millisecond)
	got := out.String()
	if !strings.HasSuffix(got, "webdiskstat: expected failure\n") {
		t.Fatalf("error output was overwritten: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	if got := humanBytes(2048); got != "2.0 KB" {
		t.Fatalf("got %q", got)
	}
}
