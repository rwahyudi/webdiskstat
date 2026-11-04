package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func compactRendererFixture(t *testing.T) *compactDiffReport {
	t.Helper()
	dir := t.TempDir()
	before := `[1,2,{"progname":"gdu"},[{"name":"/data","dsize":4},[{"name":"stable","dsize":4},{"name":"same.txt","dsize":8}],{"name":"change.txt","dsize":16}]]`
	after := strings.Replace(before, `"dsize":16`, `"dsize":32`, 1)
	b, a := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	if err := os.WriteFile(b, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := buildCompactDiffReport(b, a, strings.NewReader(""), false)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestCompactDiffRendererStreamsPlainPayloadAndAtomicFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	report := compactRendererFixture(t)
	var want bytes.Buffer
	if err := report.WriteBinary(&want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, "report.html")
	gotPath, count, err := writeCompactDiffReport(path, io.Discard, report, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != int64(len(document)) {
		t.Fatalf("byte count = %d, file length = %d", count, len(document))
	}
	if mode := fileMode(t, path); mode != 0600 {
		t.Fatalf("new file mode = %04o, want 0600", mode)
	}
	assertDiffFooterMetadata(t, string(document))
	payload := diffPayloadFromHTML(t, string(document))
	if payload["format"] != "gds-diff-binary-v2" {
		t.Fatalf("payload format = %v, want gds-diff-binary-v2", payload["format"])
	}
	encoded, ok := payload["payload"].(string)
	if !ok {
		t.Fatal("payload is not a string")
	}
	compressed, err := base85DecodeBytes(encoded, int(payload["length"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, want.Bytes()) {
		t.Fatal("streamed payload differs from compact WriteBinary output")
	}
	if !bytes.HasPrefix(decoded, []byte(compactDiffBinaryMagic)) {
		t.Fatalf("binary payload is missing magic: %q", decoded[:min(len(decoded), 8)])
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "webdiskstat-diff-") || strings.HasPrefix(entry.Name(), ".webdiskstat-diff-") {
			t.Errorf("temporary file leaked: %s", entry.Name())
		}
	}
}

func TestCompactDiffRendererStdoutAndPreservedMode(t *testing.T) {
	report := compactRendererFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.html")
	if err := os.WriteFile(path, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeCompactDiffReport(path, io.Discard, report, nil); err != nil {
		t.Fatal(err)
	}
	if mode := fileMode(t, path); mode != 0640 {
		t.Fatalf("replacement mode = %04o, want 0640", mode)
	}
	var stdout bytes.Buffer
	gotPath, count, err := writeCompactDiffReport("-", &stdout, report, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "<stdout>" || count != int64(stdout.Len()) {
		t.Fatalf("stdout result = (%q,%d), length %d", gotPath, count, stdout.Len())
	}
	assertDiffFooterMetadata(t, stdout.String())
}

func TestCompactDiffRendererEncryptedCompatibility(t *testing.T) {
	report := compactRendererFixture(t)
	password := "compact renderer password"
	var output bytes.Buffer
	_, count, err := writeCompactDiffReport("-", &output, report, &password)
	if err != nil {
		t.Fatal(err)
	}
	if count != int64(output.Len()) {
		t.Fatalf("byte count = %d, output length = %d", count, output.Len())
	}
	assertDiffFooterMetadata(t, output.String())
	payload := diffPayloadFromHTML(t, output.String())
	if payload["format"] != "gds-diff-binary-v2" || payload["algorithm"] != encryptionAlgorithm || payload["encrypted"] != true {
		t.Fatalf("unexpected encrypted envelope: %v", payload)
	}
	if payload["encoding"] != "base85" || payload["kdf"] != "PBKDF2-SHA256" || payload["aad"] != encryptionAAD {
		t.Fatalf("unexpected encrypted payload metadata: %v", payload)
	}
	if strings.Contains(output.String(), password) || strings.Contains(output.String(), `"path":"/data`) {
		t.Fatal("encrypted report leaks comparison data")
	}
}

func TestCompactDiffBinarySparseLayoutAndRootOverride(t *testing.T) {
	report := compactRendererFixture(t)
	// Add a path that cannot be derived from its parent's path and a distinct
	// before timestamp to verify overrides and sparse metadata references.
	root := &report.rows[report.root]
	root.fullPath = "/noncanonical root Ω"
	if len(report.rows) < 2 {
		t.Fatal("fixture has no child")
	}
	child := &report.rows[int(root.firstChild)]
	child.beforeMTime = "before-time"
	child.afterMTime = "after-time"
	child.status = compactStatusChanged
	child.beforeType = compactTypeFile
	child.afterType = compactTypeDir
	child.beforeItems, child.afterItems = 2, 3
	for c := root.firstChild; c >= 0; c = report.rows[c].nextSibling {
		if report.rows[c].afterType == compactTypeDir && c != root.firstChild {
			unchangedDir := &report.rows[c]
			unchangedDir.beforeItems = unchangedDir.afterItems + 1
			unchangedDir.status = compactStatusUnchanged
			break
		}
	}
	var output bytes.Buffer
	if err := report.WriteBinary(&output); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte(compactDiffBinaryMagic)) {
		t.Fatal("missing compact binary magic")
	}
	// Parse enough of the stream to assert canonical metadata keys, string
	// interning (including UTF-8), node count, and sparse overlay count.
	reader := bytes.NewReader(output.Bytes()[len(compactDiffBinaryMagic):])
	metadataLen := readTestUvarint(t, reader)
	metadata := make([]byte, metadataLen)
	if _, err := io.ReadFull(reader, metadata); err != nil {
		t.Fatal(err)
	}
	var metadataObject map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &metadataObject); err != nil {
		t.Fatal(err)
	}
	if _, ok := metadataObject["root"]; ok {
		t.Fatalf("unexpected metadata JSON: %s", metadata)
	}
	if _, ok := metadataObject["statusCounts"]; !ok {
		t.Fatalf("metadata missing statusCounts: %s", metadata)
	}
	stringCount := readTestUvarint(t, reader)
	seenUnicode, seenTime := false, false
	for i := uint64(0); i < stringCount; i++ {
		n := readTestUvarint(t, reader)
		value := make([]byte, n)
		if _, err := io.ReadFull(reader, value); err != nil {
			t.Fatal(err)
		}
		seenUnicode = seenUnicode || bytes.Contains(value, []byte("Ω"))
		seenTime = seenTime || bytes.Equal(value, []byte("before-time"))
	}
	if !seenUnicode || !seenTime {
		t.Fatalf("string table missing unicode/overlay strings: unicode=%v time=%v", seenUnicode, seenTime)
	}
	nodes := readTestUvarint(t, reader)
	if nodes != uint64(report.Count) {
		t.Fatalf("emitted nodes = %d, want %d", nodes, report.Count)
	}
	// Skip node base records using child counts to preserve the actual encoded
	// record layout; each node is exactly eight uvarints.
	for i := uint64(0); i < nodes; i++ {
		for field := 0; field < 8; field++ {
			_ = readTestUvarint(t, reader)
		}
	}
	overlays := readTestUvarint(t, reader)
	if overlays == 0 || overlays > nodes {
		t.Fatalf("overlay count = %d for %d nodes", overlays, nodes)
	}
}

func readTestUvarint(t *testing.T, r io.ByteReader) uint64 {
	t.Helper()
	v, err := binary.ReadUvarint(r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCompactDiffBinaryPropagatesWriterErrorsAndRejectsNegativeSize(t *testing.T) {
	report := compactRendererFixture(t)
	if err := report.WriteBinary(zeroWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v, want %v", err, io.ErrShortWrite)
	}
	if err := report.WriteBinary(errorWriter{err: io.ErrClosedPipe}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error = %v, want %v", err, io.ErrClosedPipe)
	}
	report.rows[report.root].after = -1
	if err := report.WriteBinary(io.Discard); err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Fatalf("negative size error = %v, want negative-size validation", err)
	}
}

func TestCompactDiffBinaryPreservesEmptyDirRemovalTypesAndOverlayIndices(t *testing.T) {
	report := &compactDiffReport{
		Complete: true, Count: 4, root: 0,
		rows: []compactDiffRow{
			{name: "root", fullPath: "/", before: 1 << 40, after: 1 << 40, beforeItems: 1, afterItems: 3, beforeType: compactTypeDir, afterType: compactTypeDir, status: compactStatusUnchanged, firstChild: 1, nextSibling: -1},
			{name: "empty", fullPath: "/empty", afterType: compactTypeDir, afterMTime: "created", status: compactStatusAdded, firstChild: -1, nextSibling: 2},
			{name: "gone", fullPath: "/gone", before: 5, beforeFiles: 1, beforeType: compactTypeFile, status: compactStatusRemoved, firstChild: -1, nextSibling: 3},
			{name: "changed", fullPath: "/explicit/noncanonical", before: 1, after: 2, beforeType: compactTypeFile, afterType: compactTypeDir, beforeMTime: "", afterMTime: "after", status: compactStatusChanged, firstChild: -1, nextSibling: -1},
		},
	}
	var output bytes.Buffer
	if err := report.WriteBinary(&output); err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(output.Bytes()[len(compactDiffBinaryMagic):])
	metadataLength := readTestUvarint(t, reader)
	if _, err := reader.Seek(int64(metadataLength), io.SeekCurrent); err != nil {
		t.Fatal(err)
	}
	stringCount := readTestUvarint(t, reader)
	stringsTable := make([]string, stringCount+1)
	for i := uint64(1); i <= stringCount; i++ {
		n := readTestUvarint(t, reader)
		value := make([]byte, n)
		if _, err := io.ReadFull(reader, value); err != nil {
			t.Fatal(err)
		}
		stringsTable[i] = string(value)
	}
	stringAt := func(ref uint64) string {
		if ref >= uint64(len(stringsTable)) {
			t.Fatalf("string reference %d is out of range", ref)
		}
		return stringsTable[ref]
	}
	nodeCount := readTestUvarint(t, reader)
	if nodeCount != 4 {
		t.Fatalf("node count = %d, want 4", nodeCount)
	}
	base := make([][8]uint64, nodeCount)
	for i := range base {
		for field := range base[i] {
			base[i][field] = readTestUvarint(t, reader)
		}
	}
	if stringAt(base[0][1]) != "/" || stringAt(base[1][1]) != "" || stringAt(base[3][1]) != "/explicit/noncanonical" {
		t.Fatalf("unexpected root/derived/override paths: %q %q %q", stringAt(base[0][1]), stringAt(base[1][1]), stringAt(base[3][1]))
	}
	if base[0][3] != 1<<40 {
		t.Fatalf("64-bit size truncated: got %d", base[0][3])
	}
	if base[2][2] != 0 {
		t.Fatalf("removed node after type = %d, want absent (0)", base[2][2])
	}
	overlayCount := readTestUvarint(t, reader)
	if overlayCount != 4 {
		t.Fatalf("overlay count = %d, want 4", overlayCount)
	}
	overlays := make([][6]uint64, overlayCount)
	indices := make([]uint64, overlayCount)
	previous := uint64(0)
	for i := range overlays {
		delta := readTestUvarint(t, reader)
		if i == 0 {
			indices[i] = delta
		} else {
			if delta == 0 {
				t.Fatalf("overlay %d has nonpositive index delta (previous index %d)", i, previous)
			}
			indices[i] = previous + delta
		}
		previous = indices[i]
		for field := range overlays[i] {
			overlays[i][field] = readTestUvarint(t, reader)
		}
	}
	if indices[0] != 0 || indices[1] != 1 || indices[2] != 2 || indices[3] != 3 {
		t.Fatalf("overlay indices = %v, want [0 1 2 3]", indices)
	}
	if overlays[0][3] != 1 || overlays[0][5] != uint64(compactStatusUnchanged) {
		t.Fatalf("unchanged parent overlay = %v; want beforeItems=1 and status=0", overlays[0])
	}
	if overlays[1][0] != 0 || overlays[1][4] != 0 || stringAt(base[1][6]) != "created" {
		t.Fatalf("added empty directory lost absent-before/after-mtime values: overlay=%v", overlays[1])
	}
	if overlays[2][0] != uint64(compactTypeFile) || base[2][2] != 0 {
		t.Fatalf("removed subtree types = before %d after %d", overlays[2][0], base[2][2])
	}
	if overlays[3][0] != uint64(compactTypeFile) || base[3][2] != uint64(compactTypeDir) || overlays[3][4] != 0 || base[3][6] == 0 {
		t.Fatalf("file-to-directory/empty-before-mtime record mismatch: overlay=%v base=%v", overlays[3], base[3])
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("unexpected trailing bytes: %v", err)
	}
}

func TestCompactDiffRendererFailuresPreserveOutputAndCleanTemps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	path := filepath.Join(tmp, "keep.html")
	if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := &compactDiffReport{rows: []compactDiffRow{{name: "root"}}, root: 3}
	if _, _, err := writeCompactDiffReport(path, io.Discard, bad, nil); err == nil {
		t.Fatal("expected serialization error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep me" {
		t.Fatalf("failed render replaced output: %q", data)
	}
	gotPath, count, err := writeCompactDiffReport("-", failingWriter{}, compactRendererFixture(t), nil)
	if err == nil || gotPath != "<stdout>" || count != 0 {
		t.Fatalf("failed stdout result = (%q,%d,%v)", gotPath, count, err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "webdiskstat-diff-") || strings.HasPrefix(entry.Name(), ".webdiskstat-diff-") {
			t.Errorf("temporary file leaked after failure: %s", entry.Name())
		}
	}
}

func TestBase85ChunkWriterMatchesLegacyAcrossPartialGroups(t *testing.T) {
	lengths := []int{1, 2, 3, 4, 5, 65539}
	for _, length := range lengths {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			input := make([]byte, length)
			for i := range input {
				input[i] = byte((i*31 + i/7) % 256)
			}
			var got bytes.Buffer
			writer := &base85ChunkWriter{dst: &got}
			chunks := [...]int{1, 3, 7}
			for offset, index := 0, 0; offset < len(input); index++ {
				n := chunks[index%len(chunks)]
				if n > len(input)-offset {
					n = len(input) - offset
				}
				written, err := writer.Write(input[offset : offset+n])
				if err != nil || written != n {
					t.Fatalf("Write() = (%d,%v), want (%d,nil)", written, err, n)
				}
				offset += n
			}
			if err := writer.Flush(); err != nil {
				t.Fatal(err)
			}
			firstFlush := got.String()
			if err := writer.Flush(); err != nil {
				t.Fatal(err)
			}
			if got.String() != firstFlush {
				t.Fatal("repeated Flush duplicated a partial base85 group")
			}
			want := base85Encode(input)
			if got.String() != want {
				t.Fatalf("streamed encoding differs from legacy encoder for %d bytes", length)
			}
			decoded, err := base85DecodeBytes(got.String(), len(input))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded, input) {
				t.Fatalf("decode did not round-trip %d bytes", length)
			}
		})
	}
}

func TestBase85ChunkWriterPropagatesShortWriteAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{name: "short write", writer: zeroWriter{}, want: io.ErrShortWrite},
		{name: "writer error", writer: errorWriter{err: io.ErrClosedPipe}, want: io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &base85ChunkWriter{dst: tc.writer}
			if n, err := writer.Write([]byte{1, 2, 3}); n != 3 || err != nil {
				t.Fatalf("buffering Write() = (%d,%v), want (3,nil)", n, err)
			}
			if err := writer.Flush(); !errors.Is(err, tc.want) {
				t.Fatalf("Flush() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCompressedLimitWriterBoundary(t *testing.T) {
	var output bytes.Buffer
	writer := &compressedLimitWriter{writer: &output, remaining: 3}
	if n, err := writer.Write([]byte{1, 2, 3}); err != nil || n != 3 {
		t.Fatalf("boundary Write() = (%d,%v), want (3,nil)", n, err)
	}
	if n, err := writer.Write([]byte{4}); err == nil || n != 0 {
		t.Fatalf("over-limit Write() = (%d,%v), want an error", n, err)
	}
	if !bytes.Equal(output.Bytes(), []byte{1, 2, 3}) {
		t.Fatalf("underlying bytes = %v, want [1 2 3]", output.Bytes())
	}
}

func TestCompactDiffStagingFileStaysPrivateUntilPublish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.html")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	file, tempPath, _, finalMode, err := compactDiffOutputTemp(path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempPath)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if mode := fileMode(t, tempPath); mode != 0600 {
		t.Fatalf("staging mode = %04o, want private mode 0600", mode)
	}
	if finalMode != 0644 {
		t.Fatalf("final mode = %04o, want preserved mode 0644", finalMode)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
