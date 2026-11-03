package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReportPreservesOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	if err := os.WriteFile(path, []byte("previous report"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("render failed")
	render := func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial report")
		return wantErr
	}
	if _, err := writeAtomicReport(path, io.Discard, render); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "previous report" {
		t.Fatalf("existing output was changed: %q, %v", contents, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
	var stdout bytes.Buffer
	if _, err := writeAtomicReport("-", &stdout, render); !errors.Is(err, wantErr) || stdout.Len() != 0 {
		t.Fatalf("failed render wrote stdout: %q, %v", stdout.String(), err)
	}
}

func TestWriteAtomicReportPublishesCompletedOutput(t *testing.T) {
	render := func(w io.Writer) error { _, err := io.WriteString(w, "complete report"); return err }
	path := filepath.Join(t.TempDir(), "nested", "report.html")
	if _, err := writeAtomicReport(path, io.Discard, render); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "complete report" {
		t.Fatalf("published output = %q, %v", contents, err)
	}
	var stdout bytes.Buffer
	if _, err := writeAtomicReport("-", &stdout, render); err != nil || stdout.String() != "complete report" {
		t.Fatalf("stdout = %q, %v", stdout.String(), err)
	}
}

func TestWriteAtomicReportKeepsPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	render := func(w io.Writer) error { _, err := io.WriteString(w, "complete"); return err }
	for _, existing := range []bool{false, true} {
		if existing {
			if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := writeAtomicReport(path, io.Discard, render); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private output permissions changed: %v, %v", info, err)
		}
	}
}
