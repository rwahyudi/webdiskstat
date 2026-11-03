package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompactDiffMatchesLegacyNativeSequence(t *testing.T) {
	dir := t.TempDir()
	before := `[1,2,{"progname":"gdu","root":"/data","timestamp":"2026-10-01"},[{"name":"/data","dsize":3},[{"name":"sub","dsize":2}, {"name":"same.txt","dsize":4}],{"name":"change.txt","dsize":8,"mtime":1}]]`
	after := strings.Replace(before, `"dsize":8`, `"dsize":12`, 1)
	beforePath, afterPath := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	if err := os.WriteFile(beforePath, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(afterPath, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	for _, complete := range []bool{false, true} {
		got, err := buildCompactDiffReport(beforePath, afterPath, strings.NewReader(""), complete)
		if err != nil {
			t.Fatal(err)
		}
		decode := func(value string) (*Node, diffMetadata) {
			t.Helper()
			var raw any
			d := json.NewDecoder(strings.NewReader(value))
			d.UseNumber()
			if err := d.Decode(&raw); err != nil {
				t.Fatal(err)
			}
			n, m, err := normalizeGDUForDiff(raw)
			if err != nil {
				t.Fatal(err)
			}
			return n, m
		}
		legacyBefore, bm := decode(before)
		legacyAfter, am := decode(after)
		legacy, err := buildDiffReportWithOptions(legacyBefore, legacyAfter, beforePath, afterPath, diffOptions{Complete: complete})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err = got.WriteJSON(&output); err != nil {
			t.Fatal(err)
		}
		var decoded DiffReport
		if err = json.Unmarshal(output.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		legacy.Before.CapturedAt = bm.capturedAt
		legacy.After.CapturedAt = am.capturedAt
		if !reflect.DeepEqual(decoded, *legacy) {
			t.Fatalf("compact output differs from legacy:\ncompact=%+v\nlegacy=%+v", decoded, *legacy)
		}
		if decoded.Before.Root != "/data" || decoded.After.CapturedAt != "2026-10-01" {
			t.Fatalf("snapshot metadata: %+v %+v", decoded.Before, decoded.After)
		}
		if got.Count != countDiffTree(decoded.Root) {
			t.Fatalf("emitted count %d != output tree count %d", got.Count, countDiffTree(decoded.Root))
		}
		if decoded.Increased != 4 || decoded.StatusCounts["grown"] != 2 {
			t.Fatalf("unexpected aggregates: increased=%d statuses=%v", decoded.Increased, decoded.StatusCounts)
		}
		if complete && decoded.Root.Children == nil {
			t.Fatal("complete report omitted unchanged descendants")
		}
		if !complete && countDiffTree(decoded.Root) != 2 {
			t.Fatalf("changes-only emitted %d nodes, want root and changed leaf", countDiffTree(decoded.Root))
		}
	}
}

func countDiffTree(n *DiffNode) int {
	if n == nil {
		return 0
	}
	count := 1
	for _, c := range n.Children {
		count += countDiffTree(c)
	}
	return count
}

type compactFailWriter struct{}

func (compactFailWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestCompactDiffWriterPropagatesErrors(t *testing.T) {
	fixture := `[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"f","dsize":1}]]`
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := os.WriteFile(p, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := buildCompactDiffReport(p, p, strings.NewReader(""), true)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.WriteJSON(compactFailWriter{}); err == nil {
		t.Fatal("expected writer error")
	}
}

func TestCompactDiffNativeEdgeCasesMatchLegacy(t *testing.T) {
	fixtures := []string{
		`[1,2,{"progname":"gdu","root":"/actual","timestamp":1730000000},[{"name":"root"},{"name":"file","dsize":2}]]`,
		`[1,2,{"progname":"gdu","root":"/root"},[{"name":"root"},[{"name":"empty.txt","mime":"application/x-custom","dsize":0}]]]`,
		`[1,2,{"progname":"gdu"},[{"name":"/r","unknown":[{"name":"nested","dsize":3}]}]]`,
		`[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"d","unknown":[{}]}]]`,
		`[1,2,{"progname":"gdu"},[{"name":"/r"},{"path":"/r/f","dsize":1}]]`,
	}
	dir := t.TempDir()
	for i, fixture := range fixtures {
		p := filepath.Join(dir, fmt.Sprintf("fixture-%d.json", i))
		if err := os.WriteFile(p, []byte(fixture), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := buildCompactDiffReport(p, p, strings.NewReader(""), true)
		if err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		var raw any
		dec := json.NewDecoder(strings.NewReader(fixture))
		dec.UseNumber()
		if err = dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		root, meta, err := normalizeGDUForDiff(raw)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := buildDiffReportWithOptions(root, root, p, p, diffOptions{Complete: true})
		if err != nil {
			t.Fatal(err)
		}
		legacy.Before.CapturedAt = meta.capturedAt
		legacy.After.CapturedAt = meta.capturedAt
		var out bytes.Buffer
		if err = got.WriteJSON(&out); err != nil {
			t.Fatal(err)
		}
		var decoded DiffReport
		if err = json.Unmarshal(out.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, *legacy) {
			t.Fatalf("fixture %d differs from legacy:\ncompact=%+v\nlegacy=%+v", i, decoded, *legacy)
		}
		if got.Count != countDiffTree(decoded.Root) {
			t.Fatalf("fixture %d count=%d output=%d", i, got.Count, countDiffTree(decoded.Root))
		}
	}
}

func TestCompactDiffDuplicateRecognizedKeyFallsBackLikeJSONMap(t *testing.T) {
	dir := t.TempDir()
	before := `[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"f","dsize":1,"dsize":2}]]`
	after := `[1,2,{"progname":"gdu"},[{"name":"/r"},{"name":"f","dsize":2}]]`
	bp, ap := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	if err := os.WriteFile(bp, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ap, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := buildCompactDiffReport(bp, ap, strings.NewReader(""), true)
	if err != nil {
		t.Fatal(err)
	}
	var braw, araw any
	bd := json.NewDecoder(strings.NewReader(before))
	bd.UseNumber()
	if err = bd.Decode(&braw); err != nil {
		t.Fatal(err)
	}
	ad := json.NewDecoder(strings.NewReader(after))
	ad.UseNumber()
	if err = ad.Decode(&araw); err != nil {
		t.Fatal(err)
	}
	broot, bmeta, err := normalizeGDUForDiff(braw)
	if err != nil {
		t.Fatal(err)
	}
	aroot, ameta, err := normalizeGDUForDiff(araw)
	if err != nil {
		t.Fatal(err)
	}
	want, err := buildDiffReportWithOptions(broot, aroot, bp, ap, diffOptions{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	want.Before.CapturedAt = bmeta.capturedAt
	want.After.CapturedAt = ameta.capturedAt
	var out bytes.Buffer
	if err = got.WriteJSON(&out); err != nil {
		t.Fatal(err)
	}
	var actual DiffReport
	if err = json.Unmarshal(out.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, *want) {
		t.Fatalf("duplicate-key report differs:\nactual=%+v\nwant=%+v", actual, *want)
	}
}

func TestCompactExportMetadataDiscardsUnknownFieldsAndHardLimitsRetainedMetadata(t *testing.T) {
	var input strings.Builder
	input.WriteString(`{"progname":" gdu ","timestamp":123`)
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&input, `,"unknown-%d":"discard-me"`, i)
	}
	input.WriteByte('}')
	d := json.NewDecoder(strings.NewReader(input.String()))
	d.UseNumber()
	meta, err := decodeCompactObject(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 2 || compactScanner(meta) != "gdu" || compactScalar(meta, "timestamp") != "123" {
		t.Fatalf("retained metadata=%v", meta)
	}
	large := strings.Repeat("x", 600<<10)
	fixture := `[1,2,{"progname":"gdu","timestamp":"` + large + `","timestamp":"` + large + `"},[{"name":"/r"}]]`
	path := filepath.Join(t.TempDir(), "large-meta.json")
	if err = os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = buildCompactDiffReport(path, path, strings.NewReader(""), false); err == nil || !strings.Contains(err.Error(), "retained-metadata limit") {
		t.Fatalf("expected hard metadata budget failure, got %v", err)
	}
}

type compactCountingReader struct {
	r    io.Reader
	read int
}

func (c *compactCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

func TestCompactTrailingJSONIsRejectedWithoutDecodingItsBody(t *testing.T) {
	valid := `[1,2,{"progname":"gdu"},[{"name":"/r"}]]`
	input := valid + `["` + strings.Repeat("x", 4<<20) + `"]`
	counted := &compactCountingReader{r: strings.NewReader(input)}
	if _, err := readCompactSnapshot("-", counted, "baseline"); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("expected trailing-value rejection, got %v", err)
	}
	if counted.read >= len(input)/2 {
		t.Fatalf("trailing value body was read: %d of %d bytes", counted.read, len(input))
	}
}

func TestCompactDiffRejectsDoubleStdinAndCompatPathExplosion(t *testing.T) {
	if _, err := buildCompactDiffReport("-", "-", strings.NewReader(""), false); err == nil {
		t.Fatal("expected duplicate stdin rejection")
	}
	children := make([]any, 10000)
	for i := range children {
		children[i] = json.Number("1")
	}
	root := map[string]any{"name": "/" + strings.Repeat("x", 8192), "children": children}
	if err := compactCompatibilityPreflight(root, "test"); err == nil || !strings.Contains(err.Error(), "path budget") {
		t.Fatalf("expected path budget error, got %v", err)
	}
}

func TestCompactJSONGuardBoundsStringBeforeDecoderMaterializesIt(t *testing.T) {
	guard := &compactJSONGuard{r: strings.NewReader(`"` + strings.Repeat("x", compactTokenStringLimit+8) + `"`), stringLimit: compactTokenStringLimit, depthLimit: compactMaxDepth}
	_, err := io.Copy(io.Discard, guard)
	if err == nil || !strings.Contains(err.Error(), "string token exceeds") {
		t.Fatalf("expected lexical string guard error, got %v", err)
	}
	numbers := &compactJSONGuard{r: strings.NewReader(strings.Repeat("7", compactTokenStringLimit+8)), numericLimit: compactTokenStringLimit, depthLimit: compactMaxDepth}
	_, err = io.Copy(io.Discard, numbers)
	if err == nil || !strings.Contains(err.Error(), "numeric token exceeds") {
		t.Fatalf("expected lexical numeric guard error, got %v", err)
	}
}
