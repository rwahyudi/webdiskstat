package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsEmptyPassword(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--password", ""}, strings.NewReader(`{"name":"root"}`), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--password must not be empty") {
		t.Fatalf("stderr missing password error: %s", stderr.String())
	}
}

func TestRunWritesStdoutReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-o", "-"}, strings.NewReader(`{"name":"root","children":[{"name":"a.txt","size":1}]}`), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "const REPORT_DATA_PAYLOAD = ") {
		t.Fatalf("stdout does not look like a report")
	}
	if strings.Contains(stdout.String(), "a.txt") {
		t.Fatalf("raw filename should be inside compressed payload, not directly in HTML")
	}
}
