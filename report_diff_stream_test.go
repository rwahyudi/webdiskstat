package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func streamFixture(t *testing.T, records []diskDiffRecord) *diskDiffReport {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := json.NewEncoder(f).Encode(record); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return &diskDiffReport{
		Before:   DiffSnapshot{Source: "before", Root: "/root", Size: 10},
		After:    DiffSnapshot{Source: "after", Root: "/root", Size: 12},
		Complete: true, Increased: 2, StatusCounts: map[string]int{"grown": 1},
		Rankings: DiffRankings{}, RecordsPath: path, Count: int64(len(records)),
	}
}

func streamRecords() []diskDiffRecord {
	return []diskDiffRecord{
		{Depth: 0, Node: DiffNode{Name: "root", Path: "/root", Type: "dir", Status: "unchanged"}},
		{Depth: 1, Node: DiffNode{Name: `</script><script>bad()</script>`, Path: "/root/x", Type: "file", After: 12, Delta: 2, Status: "grown"}},
	}
}

func recordsFromDiffTree(node *DiffNode, depth int, records *[]diskDiffRecord) {
	copyNode := *node
	copyNode.Children = nil
	*records = append(*records, diskDiffRecord{Depth: depth, Node: copyNode})
	for _, child := range node.Children {
		recordsFromDiffTree(child, depth+1, records)
	}
}

func TestWriteDiskDiffReportMatchesLegacyFixture(t *testing.T) {
	want := diffReportFixture()
	var records []diskDiffRecord
	recordsFromDiffTree(want.Root, 0, &records)
	report := streamFixture(t, records)
	report.Before, report.After = want.Before, want.After
	report.Complete, report.Increased, report.Decreased = want.Complete, want.Increased, want.Decreased
	report.StatusCounts, report.Rankings = want.StatusCounts, want.Rankings

	var html bytes.Buffer
	if err := writeDiskDiffReport(&html, report, nil); err != nil {
		t.Fatal(err)
	}
	assertDiffFooterMetadata(t, html.String())
	payloadMatch := regexp.MustCompile(`(?s)<script id="diffData" type="application/json">(.*?)</script>`).FindSubmatch(html.Bytes())
	if len(payloadMatch) != 2 {
		t.Fatal("missing diff payload")
	}
	var payload struct {
		Length  int    `json:"length"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(payloadMatch[1], &payload); err != nil {
		t.Fatal(err)
	}
	compressed, err := base85DecodeBytes(payload.Payload, payload.Length)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	var got DiffReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatal("streamed report does not round-trip to the legacy fixture")
	}
}

func TestWriteDiskDiffReportRoundTripAndFooterSize(t *testing.T) {
	report := streamFixture(t, streamRecords())
	var html bytes.Buffer
	if err := writeDiskDiffReport(&html, report, nil); err != nil {
		t.Fatal(err)
	}
	assertDiffFooterMetadata(t, html.String())
	match := regexp.MustCompile(`(?s)<script id="diffData" type="application/json">(.*?)</script>`).FindSubmatch(html.Bytes())
	if len(match) != 2 {
		t.Fatal("missing payload envelope")
	}
	var payload struct {
		Format    string `json:"format"`
		Encoding  string `json:"encoding"`
		Encrypted bool   `json:"encrypted"`
		Length    int    `json:"length"`
		Payload   string `json:"payload"`
	}
	if err := json.Unmarshal(match[1], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Format != "gds-diff-json-v1" || payload.Encoding != "base85" || payload.Encrypted {
		t.Fatalf("unexpected envelope: %+v", payload)
	}
	compressed, err := base85DecodeBytes(payload.Payload, payload.Length)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	var got DiffReport
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatal(err)
	}
	want := &DiffReport{Before: report.Before, After: report.After, Root: &DiffNode{
		Name: "root", Path: "/root", Type: "dir", Status: "unchanged", Children: []*DiffNode{{Name: `</script><script>bad()</script>`, Path: "/root/x", Type: "file", After: 12, Delta: 2, Status: "grown"}},
	}, Complete: report.Complete, Increased: report.Increased, Decreased: report.Decreased, StatusCounts: report.StatusCounts, Rankings: report.Rankings}
	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("streamed JSON differs from expected report\n got: %#v\nwant: %#v", &got, want)
	}
	if strings.Contains(html.String(), `</script><script>bad()`) {
		t.Fatal("unescaped script terminator in HTML")
	}
	size := regexp.MustCompile(`HTML file: ([^<]+)`).FindSubmatch(html.Bytes())
	if len(size) != 2 || string(size[1]) != formatReportFileSize(html.Len())[len("HTML file: "):] {
		t.Fatalf("footer size does not match final HTML length: %q / %d", size, html.Len())
	}
}

func TestWriteDiskDiffReportRejectsBadDepthAndPropagatesWriterErrors(t *testing.T) {
	report := streamFixture(t, []diskDiffRecord{{Depth: 1, Node: DiffNode{Name: "bad"}}})
	if err := writeDiskDiffReport(io.Discard, report, nil); err == nil {
		t.Fatal("expected invalid-root-depth error")
	}

	report = streamFixture(t, streamRecords())
	if err := writeDiskDiffReport(failWriter{}, report, nil); err == nil || !strings.Contains(err.Error(), "write failure") {
		t.Fatalf("expected output writer error, got %v", err)
	}
}

func TestWriteDiskDiffReportPreservesEmptyRootAndEncryptedEnvelope(t *testing.T) {
	root := streamFixture(t, []diskDiffRecord{{Depth: 0, Node: DiffNode{Name: "empty", Path: "/root", Type: "dir", Status: "unchanged"}}})
	var plain bytes.Buffer
	if err := writeDiskDiffReport(&plain, root, nil); err != nil {
		t.Fatal(err)
	}
	assertDiffFooterMetadata(t, plain.String())
	match := regexp.MustCompile(`(?s)<script id="diffData" type="application/json">(.*?)</script>`).FindSubmatch(plain.Bytes())
	if len(match) != 2 {
		t.Fatal("missing plain payload envelope")
	}
	var envelope map[string]any
	if err := json.Unmarshal(match[1], &envelope); err != nil {
		t.Fatal(err)
	}
	compressed, err := base85DecodeBytes(envelope["payload"].(string), int(envelope["length"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DiffReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Root == nil || decoded.Root.Name != "empty" || len(decoded.Root.Children) != 0 {
		t.Fatalf("empty root was not preserved: %#v", decoded.Root)
	}

	password := "test-password"
	var encrypted bytes.Buffer
	if err := writeDiskDiffReport(&encrypted, root, &password); err != nil {
		t.Fatal(err)
	}
	assertDiffFooterMetadata(t, encrypted.String())
	match = regexp.MustCompile(`(?s)<script id="diffData" type="application/json">(.*?)</script>`).FindSubmatch(encrypted.Bytes())
	if len(match) != 2 {
		t.Fatal("missing encrypted payload envelope")
	}
	if err := json.Unmarshal(match[1], &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["format"] != "gds-diff-json-v1" || envelope["encrypted"] != true || envelope["algorithm"] != encryptionAlgorithm {
		t.Fatalf("unexpected encrypted envelope: %#v", envelope)
	}
	if strings.Contains(string(encrypted.Bytes()), `</script><script>`) {
		t.Fatal("script terminator escaped into encrypted HTML")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failure") }
