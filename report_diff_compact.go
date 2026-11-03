package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	compactDiffChunkSize      = 64 * 1024
	compactDiffEncryptedLimit = 64 << 20
	compactDiffPayloadMarker  = "__WEBDISKSTAT_COMPACT_DIFF_PAYLOAD_7b4da71f__"
)

// writeCompactDiffReport streams the JSON document to a temporary gzip file,
// then streams the base85 payload into either stdout or an atomically-installed
// HTML file. The template is fixed-size; the comparison itself is never held as
// a JSON, base85, or rendered-HTML string.
func writeCompactDiffReport(output string, stdout io.Writer, report *compactDiffReport, password *string) (string, int64, error) {
	if report == nil {
		return "", 0, fmt.Errorf("diff report is nil")
	}
	compressed, err := os.CreateTemp("", "webdiskstat-diff-*.gz")
	if err != nil {
		return "", 0, err
	}
	compressedPath := compressed.Name()
	defer os.Remove(compressedPath)

	var limit io.Writer = compressed
	if password != nil {
		limit = &compressedLimitWriter{writer: compressed, remaining: compactDiffEncryptedLimit}
	}
	gz := gzip.NewWriter(limit)
	bufferedJSON := bufio.NewWriterSize(gz, compactDiffChunkSize)
	if err := report.WriteJSON(bufferedJSON); err != nil {
		_ = gz.Close()
		_ = compressed.Close()
		return "", 0, err
	}
	if err := bufferedJSON.Flush(); err != nil {
		_ = gz.Close()
		_ = compressed.Close()
		return "", 0, err
	}
	if err := gz.Close(); err != nil {
		_ = compressed.Close()
		return "", 0, err
	}
	if err := compressed.Sync(); err != nil {
		_ = compressed.Close()
		return "", 0, err
	}
	if err := compressed.Close(); err != nil {
		return "", 0, err
	}
	compressed, err = os.Open(compressedPath)
	if err != nil {
		return "", 0, err
	}
	defer compressed.Close()
	compressedInfo, err := compressed.Stat()
	if err != nil {
		return "", 0, err
	}
	if password != nil {
		if compressedInfo.Size() > compactDiffEncryptedLimit {
			return "", 0, fmt.Errorf("encrypted comparison exceeds the %d MiB compressed-data limit", compactDiffEncryptedLimit>>20)
		}
		// Retain the existing audited ChaCha20-Poly1305 format and crypto helper.
		// This bounded read is deliberately after streaming gzip serialization.
		plain := make([]byte, compressedInfo.Size())
		if _, err := io.ReadFull(compressed, plain); err != nil {
			return "", 0, err
		}
		payload, err := encryptReportData(plain, *password)
		clear(plain)
		if err != nil {
			return "", 0, err
		}
		return writeCompactDiffHTML(output, stdout, true, compressedInfo.Size(), payload, nil)
	}
	return writeCompactDiffHTML(output, stdout, false, compressedInfo.Size(), nil, compressed)
}

type compressedLimitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *compressedLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("encrypted comparison exceeds the %d MiB compressed-data limit", compactDiffEncryptedLimit>>20)
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func writeCompactDiffHTML(output string, stdout io.Writer, encrypted bool, compressedLength int64, encryptedPayload map[string]any, compressed io.Reader) (string, int64, error) {
	runtime, err := diffReportRuntime()
	if err != nil {
		return "", 0, err
	}
	themeCSS, themeControl, err := diffReportAppearance()
	if err != nil {
		return "", 0, err
	}
	browserJS, browserCSS, err := diffBrowserSupport()
	if err != nil {
		return "", 0, err
	}
	now := time.Now().Local()
	generatedISO := now.Format(time.RFC3339)
	generatedDisplay := now.Format("2006-01-02 15:04:05 MST")
	footer := diffReportFooterContent(encrypted)
	base := strings.NewReplacer("/* DIFF_FONTS */", embeddedFontCSS(), "/* DIFF_THEME_CSS */", themeCSS, "<!-- DIFF_THEME_SWITCH -->", themeControl,
		"/* NORMAL_BROWSER_CSS */", browserCSS,
		"/* DIFF_BROWSER_RUNTIME */", strings.Replace(diffBrowserRuntime, "/* NORMAL_BROWSER_FUNCTIONS */", browserJS, 1),
		"/* DIFF_RUNTIME */", runtime, "/* DIFF_PAYLOAD */", compactDiffPayloadMarker, "<!-- DIFF_FOOTER_CONTENT -->", footer,
		"DIFF_FAVICON", faviconDataURI()).Replace(diffReportTemplate)
	base = replaceGeneratedMetadata(base, generatedISO, generatedDisplay)
	marker := strings.Index(base, compactDiffPayloadMarker)
	if marker < 0 || strings.Index(base[marker+len(compactDiffPayloadMarker):], compactDiffPayloadMarker) >= 0 {
		return "", 0, fmt.Errorf("diff report payload marker missing or duplicated")
	}
	prefix, suffix := base[:marker], base[marker+len(compactDiffPayloadMarker):]
	var payloadPrefix, payloadSuffix string
	var payloadWriter func(io.Writer) error
	payloadLength := int64(0)
	if encrypted {
		encoded, ok := encryptedPayload["payload"].(string)
		if !ok {
			return "", 0, fmt.Errorf("encrypted payload is missing")
		}
		delete(encryptedPayload, "payload")
		encryptedPayload["format"] = "gds-diff-json-v1"
		encodedHeader, err := scriptJSON(encryptedPayload)
		if err != nil {
			return "", 0, err
		}
		payloadPrefix = strings.TrimSuffix(encodedHeader, "}") + `,"payload":"`
		payloadSuffix = `"}`
		payloadLength = int64(len(encoded))
		payloadWriter = func(w io.Writer) error { return writeAll(w, []byte(encoded)) }
	} else {
		header, err := scriptJSON(map[string]any{"encrypted": false, "encoding": "base85", "format": "gds-diff-json-v1", "length": compressedLength})
		if err != nil {
			return "", 0, err
		}
		payloadPrefix = strings.TrimSuffix(header, "}") + `,"payload":"`
		payloadSuffix = `"}`
		payloadLength = ((compressedLength + 3) / 4) * 5
		payloadWriter = func(w io.Writer) error {
			encoder := &base85ChunkWriter{dst: w}
			if _, err := io.Copy(encoder, compressed); err != nil {
				return err
			}
			return encoder.Flush()
		}
	}
	return writeCompactDiffBytes(output, stdout, prefix+payloadPrefix, payloadSuffix+suffix, payloadLength, payloadWriter)
}

// base85ChunkWriter retains up to three input bytes between writes and emits
// complete four-byte groups in sizeable writes instead of per-word syscalls.
type base85ChunkWriter struct {
	dst    io.Writer
	input  [compactDiffChunkSize]byte
	used   int
	output [((compactDiffChunkSize + 3) / 4) * 5]byte
}

func (w *base85ChunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := copy(w.input[w.used:], p)
		w.used += n
		p = p[n:]
		written += n
		complete := w.used / 4 * 4
		if complete == 0 {
			continue
		}
		outLen := encodeBase85Blocks(w.output[:], w.input[:complete])
		if err := writeAll(w.dst, w.output[:outLen]); err != nil {
			return written, err
		}
		copy(w.input[:], w.input[complete:w.used])
		w.used -= complete
	}
	return written, nil
}

func (w *base85ChunkWriter) Flush() error {
	if w.used == 0 {
		return nil
	}
	var block [4]byte
	copy(block[:], w.input[:w.used])
	var encoded [5]byte
	encodeBase85Block(encoded[:], block[:])
	if err := writeAll(w.dst, encoded[:]); err != nil {
		return err
	}
	w.used = 0
	return nil
}

func encodeBase85Blocks(dst, src []byte) int {
	out := 0
	for i := 0; i < len(src); i += 4 {
		encodeBase85Block(dst[out:out+5], src[i:i+4])
		out += 5
	}
	return out
}

func encodeBase85Block(dst, src []byte) {
	var value uint32
	for _, b := range src {
		value = value<<8 | uint32(b)
	}
	for i := 4; i >= 0; i-- {
		dst[i] = base85Alphabet[value%85]
		value /= 85
	}
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

var compactDiffHTMLSizePattern = regexp.MustCompile(`HTML file: [^<]+`)

func compactDiffHTMLSize(prefix, suffix string, payloadChars int64) (string, int64, error) {
	if len(compactDiffHTMLSizePattern.FindAllStringIndex(prefix, -1)) != 1 {
		return "", 0, fmt.Errorf("diff report must have exactly one HTML size label before its payload")
	}
	label := "pending"
	for i := 0; i < 12; i++ {
		footer := "HTML file: " + label
		updated := compactDiffHTMLSizePattern.ReplaceAllString(prefix, footer)
		total := int64(len(updated)) + payloadChars + int64(len(suffix))
		next := strings.TrimPrefix(formatReportFileSize(int(total)), "HTML file: ")
		if next == label {
			return updated, total, nil
		}
		label = next
	}
	return "", 0, fmt.Errorf("could not stabilize diff report size metadata")
}

func compactDiffOutputTemp(output string) (*os.File, string, string, os.FileMode, error) {
	path, err := filepath.Abs(output)
	if err != nil {
		return nil, "", "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", "", 0, err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, "", "", 0, fmt.Errorf("refusing to replace non-regular output %q", path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return nil, "", "", 0, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".webdiskstat-diff-*.tmp")
	if err != nil {
		return nil, "", "", 0, err
	}
	// CreateTemp leaves the staging file private (0600). The requested final
	// mode is applied only after all report bytes have been written and closed.
	return file, file.Name(), path, mode, nil
}

func writeCompactDiffBytes(output string, stdout io.Writer, prefix, suffix string, payloadLength int64, payload func(io.Writer) error) (string, int64, error) {
	prefix, total, err := compactDiffHTMLSize(prefix, suffix, payloadLength)
	if err != nil {
		return "", 0, err
	}
	if output == "-" {
		count := &countingWriter{writer: stdout}
		if err := writeAll(count, []byte(prefix)); err != nil {
			return "<stdout>", count.count, err
		}
		if err := payload(count); err != nil {
			return "<stdout>", count.count, err
		}
		if err := writeAll(count, []byte(suffix)); err != nil {
			return "<stdout>", count.count, err
		}
		return "<stdout>", count.count, nil
	}
	file, tempPath, path, mode, err := compactDiffOutputTemp(output)
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tempPath)
	count := &countingWriter{writer: file}
	if err := writeAll(count, []byte(prefix)); err != nil {
		file.Close()
		return "", count.count, err
	}
	if err := payload(count); err != nil {
		file.Close()
		return "", count.count, err
	}
	if err := writeAll(count, []byte(suffix)); err != nil {
		file.Close()
		return "", count.count, err
	}
	if count.count != total {
		file.Close()
		return "", count.count, fmt.Errorf("diff report wrote %d bytes, expected %d", count.count, total)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", count.count, err
	}
	if err := file.Close(); err != nil {
		return "", count.count, err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return "", count.count, err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", count.count, err
	}
	return path, count.count, nil
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}
