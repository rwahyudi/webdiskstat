package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

func TestRunPrintsVersionWithoutInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-v"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "webdiskstat "+version) || !strings.Contains(stdout.String(), "Built: ") {
		t.Fatalf("unexpected version output: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("version wrote to stderr: %s", stderr.String())
	}
}

func TestParseArgsCompleteRequiresDiffAndAcceptsFlagOrders(t *testing.T) {
	for _, args := range [][]string{
		{"--complete", "--diff", "before.json", "after.json"},
		{"--diff", "before.json", "-c", "after.json"},
	} {
		opts, err := parseArgs(args, &bytes.Buffer{})
		if err != nil || !opts.diff || !opts.complete || len(opts.inputs) != 2 {
			t.Errorf("parseArgs(%v) = %#v, %v", args, opts, err)
		}
	}
	for _, args := range [][]string{{"--complete"}, {"-c", "input.json"}} {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--complete requires --diff") {
			t.Errorf("parseArgs(%v) error = %v, want --complete requires --diff", args, err)
		}
	}
	defaultOpts, err := parseArgs([]string{"--diff", "before.json", "after.json"}, &bytes.Buffer{})
	if err != nil || defaultOpts.complete {
		t.Fatalf("default diff options = %#v, %v", defaultOpts, err)
	}
}

func TestRunDiffCompleteModeInPlainAndEncryptedPayloads(t *testing.T) {
	dir := t.TempDir()
	beforePath, afterPath := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	before := `{"name":"/data","path":"/data","dsize":1,"children":[{"name":"same","size":5}]}`
	after := `{"name":"/data","path":"/data","dsize":1,"children":[{"name":"same","size":5}]}`
	if err := os.WriteFile(beforePath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(afterPath, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		plain bool
	}{
		{name: "plain", args: []string{"--diff", "--complete", beforePath, afterPath, "-o", "-"}, plain: true},
		{name: "encrypted", args: []string{"--diff", "-c", beforePath, afterPath, "--password", "test-password", "-o", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("run exit code = %d; stderr=%s", code, stderr.String())
			}
			payload := diffPayloadFromHTML(t, stdout.String())
			if payload["encrypted"] != !tc.plain {
				t.Fatalf("payload encrypted = %v, want %v", payload["encrypted"], !tc.plain)
			}
			if tc.plain {
				compressed, err := base85DecodeBytes(payload["payload"].(string), int(payload["length"].(float64)))
				if err != nil {
					t.Fatal(err)
				}
				reader, err := gzip.NewReader(bytes.NewReader(compressed))
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				var report DiffReport
				if err := json.Unmarshal(raw, &report); err != nil {
					t.Fatal(err)
				}
				if !report.Complete || len(report.Root.Children) != 1 || report.Root.Children[0].Name != "same" {
					t.Fatalf("plain complete report = %#v", report)
				}
			}
		})
	}
}
