package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSizeLimitedReaderRejectsDataPastLimit(t *testing.T) {
	reader := newSizeLimitedReader(strings.NewReader("abcdef"), 4)
	buffer := make([]byte, 8)
	n, err := reader.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "abcd" {
		t.Fatalf("read %q, want %q", got, "abcd")
	}
	if _, err := reader.Read(buffer); !errors.Is(err, errInputTooLarge) {
		t.Fatalf("error = %v, want errInputTooLarge", err)
	}
}

func TestSizeLimitedReaderAllowsExactLimit(t *testing.T) {
	reader := newSizeLimitedReader(strings.NewReader("abcd"), 4)
	buffer := make([]byte, 8)
	n, err := reader.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "abcd" {
		t.Fatalf("read %q, want %q", got, "abcd")
	}
	if _, err := reader.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want io.EOF", err)
	}
}

func TestReadJSONRejectsTrailingValue(t *testing.T) {
	if _, err := readJSON("-", bytes.NewBufferString(`{} {}`)); err == nil {
		t.Fatal("readJSON accepted multiple JSON values")
	}
}
