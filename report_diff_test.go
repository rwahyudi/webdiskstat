package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func diffReportFixture() *DiffReport {
	root := &DiffNode{Name: "private-root", Path: "/private-root", Type: "dir", Before: 500, After: 500, BeforeFiles: 262, AfterFiles: 262, Status: "unchanged"}
	context := &DiffNode{Name: "offset", Path: "/private-root/offset", Type: "dir", Before: 100, After: 100, Status: "unchanged", Children: []*DiffNode{
		{Name: "grow.bin", Path: "/private-root/offset/grow.bin", Type: "file", Before: 0, After: 100, Delta: 100, Status: "added"},
		{Name: "shrink.bin", Path: "/private-root/offset/shrink.bin", Type: "file", Before: 100, After: 0, Delta: -100, Status: "removed"},
	}}
	root.Children = append(root.Children, context)
	for i := 0; i < 260; i++ {
		name := fmt.Sprintf("changed-%03d.bin", i)
		root.Children = append(root.Children, &DiffNode{Name: name, Path: root.Path + "/" + name, Type: "file", Before: 1, After: 2, Delta: 1, Status: "grown"})
	}
	root.Children = append(root.Children, &DiffNode{Name: "</script><script>window.diffXSS=1</script>", Path: "/private-root/=formula", Type: "file", Status: "changed"})
	rankings := DiffRankings{
		GrowingFiles:   []DiffRankingRow{{Name: "grow.bin", Path: "/private-root/offset/grow.bin", Type: "file", After: 100, Delta: 100, Status: "added"}},
		ShrinkingFiles: []DiffRankingRow{{Name: "shrink.bin", Path: "/private-root/offset/shrink.bin", Type: "file", Before: 100, Delta: -100, Status: "removed"}},
	}
	for i := 0; i < 99; i++ {
		n := root.Children[i+1]
		rankings.GrowingFiles = append(rankings.GrowingFiles, DiffRankingRow{Name: n.Name, Path: n.Path, Type: n.Type, Before: n.Before, After: n.After, Delta: n.Delta, Status: n.Status})
	}
	return &DiffReport{Before: DiffSnapshot{Source: "baseline-secret.json", Root: root.Path, Size: 500, Files: 262, Directories: 2, CapturedAt: "2026-10-31T00:00:00Z"}, After: DiffSnapshot{Source: "current-secret.json", Root: root.Path, Size: 500, Files: 262, Directories: 2}, Root: root, Increased: 360, Decreased: 100, StatusCounts: map[string]int{"added": 1, "removed": 1, "grown": 260, "changed": 1}, Rankings: rankings}
}

func diffPayloadFromHTML(t *testing.T, html string) map[string]any {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script id="diffData" type="application/json">(.*?)</script>`).FindStringSubmatch(html)
	if len(match) != 2 {
		t.Fatal("missing diff payload")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(match[1]), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestDiffReportCompressedJSONRoundTrip(t *testing.T) {
	want := diffReportFixture()
	html, err := renderDiffReport(want, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := diffPayloadFromHTML(t, html)
	if payload["format"] != "gds-diff-json-v1" || payload["encoding"] != "base85" || payload["encrypted"] != false {
		t.Fatalf("unexpected payload: %v", payload)
	}
	compressed, err := base85DecodeBytes(payload["payload"].(string), int(payload["length"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var got DiffReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatal("gzip JSON did not round-trip")
	}
	for _, forbidden := range []string{"window.diffXSS=1", "baseline-secret.json", "/* DIFF_", "https://", "http://"} {
		// SVG namespace URI is an identifier, not a network request.
		if forbidden == "http://" {
			continue
		}
		if strings.Contains(html, forbidden) {
			t.Errorf("artifact contains %q", forbidden)
		}
	}
	// The ordinary icon helper uses innerHTML only for its bundled, trusted SVG
	// library. Snapshot names/paths still use textContent (also exercised below).
	for _, required := range []string{"function pbkdf2Sha256(", "function chacha20Poly1305Decrypt(", "function bytesFromReportPayload(", "DecompressionStream", "aria-sort", "csvCell", "function renderVisibleTreeRows("} {
		if !strings.Contains(html, required) {
			t.Errorf("missing runtime feature %s", required)
		}
	}
}

func TestDiffReportEncryptedArtifact(t *testing.T) {
	password := "diff-password"
	html, err := renderDiffReport(diffReportFixture(), &password)
	if err != nil {
		t.Fatal(err)
	}
	payload := diffPayloadFromHTML(t, html)
	if payload["encrypted"] != true || payload["algorithm"] != encryptionAlgorithm || payload["format"] != "gds-diff-json-v1" {
		t.Fatalf("unexpected encryption metadata: %v", payload)
	}
	for _, secret := range []string{password, "private-root", "baseline-secret.json", "current-secret.json", "grow.bin", "window.diffXSS=1", "2026-10-31T00:00:00Z"} {
		if strings.Contains(html, secret) {
			t.Errorf("encrypted artifact leaks %q", secret)
		}
	}
}

func TestDiffReportRejectsMissingRoot(t *testing.T) {
	for _, report := range []*DiffReport{nil, {}} {
		if _, err := renderDiffReport(report, nil); err == nil {
			t.Error("expected missing-root error")
		}
	}
}

// Browser coverage is optional where Playwright or its Chromium installation is
// absent. It runs against file://, including the crypto.subtle-free fallback.
func TestDiffReportBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; browser verification skipped")
	}
	dir := t.TempDir()
	password := "browser-password"
	noChange := &DiffReport{Before: DiffSnapshot{}, After: DiffSnapshot{}, Root: &DiffNode{Name: "empty", Type: "dir", Status: "unchanged"}}
	for _, fixture := range []struct {
		name     string
		report   *DiffReport
		password *string
	}{{"plain", diffReportFixture(), nil}, {"encrypted", diffReportFixture(), &password}, {"empty", noChange, nil}} {
		html, err := renderDiffReport(fixture.report, fixture.password)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.name+".html"), []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(diffReportBrowserCheck)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const diffReportBrowserCheck = diffBrowserRegressionCheck
