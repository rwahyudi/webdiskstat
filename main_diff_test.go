package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgsDiffRequiresTwoGDUInputs(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--diff", "before.json"}, "exactly two"},
		{[]string{"--diff", "before.json", "after.json", "extra.json"}, "exactly two"},
		{[]string{"--diff", "-", "-"}, "at most once"},
		{[]string{"--diff", "--input-type", "ncdu", "before", "after"}, "gdu input only"},
	} {
		_, err := parseArgs(test.args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("parseArgs(%q) error = %v, want %q", test.args, err, test.want)
		}
	}
}

func TestParseArgsDiffAllowsOneStdin(t *testing.T) {
	opts, err := parseArgs([]string{"--diff", "-", "after.json", "-o", "-"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.inputs) != 2 || opts.inputs[0] != "-" || opts.inputs[1] != "after.json" || opts.output != "-" {
		t.Fatalf("options = %#v", opts)
	}
}

func TestRunDiffWritesReport(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.json")
	after := filepath.Join(dir, "after.json")
	if err := os.WriteFile(before, []byte(`{"name":"/data","path":"/data","dsize":1,"children":[{"name":"old","size":2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(`{"name":"/data","path":"/data","dsize":1,"children":[{"name":"new","size":2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--diff", before, after, "-o", "-"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, stderr.String())
	}
	if payload := diffPayloadFromHTML(t, stdout.String()); payload["format"] != "gds-diff-binary-v2" {
		t.Fatalf("stdout does not contain compact binary diff data: %v", payload["format"])
	}
}

func TestRunDiffReportsBaselineAndCurrentContext(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.json")
	after := filepath.Join(dir, "after.json")
	if err := os.WriteFile(before, []byte(`{"name":"/baseline","path":"/baseline"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(`{"name":"/current","path":"/current"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--diff", before, after, "-o", "-"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "snapshot roots differ") || !strings.Contains(stderr.String(), "baseline") || !strings.Contains(stderr.String(), "current") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
