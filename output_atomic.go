package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// writeAtomicReport publishes a complete report only after rendering succeeds.
// Staging stdout on disk also prevents validation failures from leaking partial HTML.
func writeAtomicReport(output string, stdout io.Writer, render func(io.Writer) error) (string, error) {
	path, dir := "<stdout>", ""
	if output != "-" {
		var err error
		path, err = filepath.Abs(output)
		if err != nil {
			return "", err
		}
		dir = filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	file, err := os.CreateTemp(dir, ".webdiskstat-report-*")
	if err != nil {
		return "", fmt.Errorf("stage report: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := render(file); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("flush report: %w", err)
	}
	if output == "-" {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		_, err := io.Copy(stdout, file)
		return path, err
	}
	// New reports remain private. When replacing an existing regular report,
	// preserve its access policy rather than making a private report public.
	if previous, err := os.Lstat(path); err == nil && previous.Mode().IsRegular() {
		if err := file.Chmod(previous.Mode().Perm()); err != nil {
			return "", err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", fmt.Errorf("publish report: %w", err)
	}
	return path, nil
}
