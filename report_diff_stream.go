package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	diffStreamRecordLimit        = 1 << 20
	diffStreamCompressedLimit    = int64(1 << 30)
	diffStreamEncryptedLimit     = int64(64 << 20)
	diffStreamRendererDepthLimit = 1024
	diffStreamPayloadMarker      = "__DIFF_STREAM_PAYLOAD__"
	diffStreamSizeMarker         = "HTML file: pending"
)

// writeDiskDiffReport streams a disk-backed comparison into a normal HTML
// report. Its only per-record memory bound is diffStreamRecordLimit; compressed
// output is kept in a private temporary file until its encoded length is known.
func writeDiskDiffReport(w io.Writer, report *diskDiffReport, password *string) error {
	if w == nil || report == nil || report.RecordsPath == "" {
		return fmt.Errorf("disk diff report and output writer are required")
	}
	compressed, err := os.CreateTemp("", "webdiskstat-diff-*.gz")
	if err != nil {
		return fmt.Errorf("create compressed diff temporary file: %w", err)
	}
	compressedName := compressed.Name()
	defer os.Remove(compressedName)
	if err := compressed.Chmod(0600); err != nil {
		compressed.Close()
		return fmt.Errorf("secure compressed diff temporary file: %w", err)
	}
	limited := &diffStreamLimitWriter{w: compressed, limit: diffStreamCompressedLimit}
	gz, err := gzip.NewWriterLevel(limited, gzip.DefaultCompression)
	if err != nil {
		compressed.Close()
		return err
	}
	if err = writeDiskDiffJSON(gz, report); err != nil {
		_ = gz.Close()
		_ = compressed.Close()
		return err
	}
	if err = gz.Close(); err != nil {
		compressed.Close()
		return fmt.Errorf("finish compressed diff report: %w", err)
	}
	if err = compressed.Sync(); err != nil {
		compressed.Close()
		return fmt.Errorf("flush compressed diff report: %w", err)
	}
	info, err := compressed.Stat()
	if err != nil {
		compressed.Close()
		return err
	}
	if err = compressed.Close(); err != nil {
		return err
	}
	if password != nil && info.Size() > diffStreamEncryptedLimit {
		return fmt.Errorf("encrypted diff report exceeds the %d MiB compressed-data limit", diffStreamEncryptedLimit>>20)
	}

	var envelope string
	if password != nil {
		file, openErr := os.Open(compressedName)
		if openErr != nil {
			return openErr
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		payload, encErr := encryptReportData(data, *password)
		if encErr != nil {
			return encErr
		}
		payload["format"] = "gds-diff-json-v1"
		envelope, err = scriptJSON(payload)
		if err != nil {
			return err
		}
	} else {
		payload, marshalErr := json.Marshal(map[string]any{
			"encrypted": false, "encoding": "base85", "format": "gds-diff-json-v1",
			"length": info.Size(), "payload": diffStreamPayloadMarker,
		})
		if marshalErr != nil {
			return marshalErr
		}
		envelope = string(payload)
	}

	static, err := diskDiffStaticTemplate(password != nil)
	if err != nil {
		return err
	}
	if strings.Count(static, diffStreamPayloadMarker) != 1 || strings.Count(static, diffStreamSizeMarker) != 1 {
		return fmt.Errorf("diff report template is missing a unique streaming marker")
	}
	staticParts := strings.SplitN(static, diffStreamPayloadMarker, 2)
	payloadParts := strings.SplitN(envelope, diffStreamPayloadMarker, 2)
	streamPayload := password == nil
	if streamPayload && len(payloadParts) != 2 {
		return fmt.Errorf("diff payload envelope is missing its stream marker")
	}
	payloadLen := int64(len(envelope))
	if streamPayload {
		payloadLen = int64(len(payloadParts[0])+len(payloadParts[1])) + ((info.Size()+3)/4)*5
	}
	sizeMarker := diffStreamSizeMarker
	baseSize := int64(len(staticParts[0])+len(staticParts[1])) + payloadLen
	for i := 0; i < 12; i++ {
		// The footer may precede the payload marker in the template. Adjust the
		// total for the label wherever it occurs, rather than assuming a suffix.
		total := baseSize + int64(len(sizeMarker)-len(diffStreamSizeMarker))
		next := formatReportFileSize(int(total))
		if next == sizeMarker {
			break
		}
		sizeMarker = next
	}
	staticParts[0] = strings.Replace(staticParts[0], diffStreamSizeMarker, sizeMarker, 1)
	staticParts[1] = strings.Replace(staticParts[1], diffStreamSizeMarker, sizeMarker, 1)

	output := bufio.NewWriterSize(w, 64*1024)
	if err := writeAll(output, []byte(staticParts[0])); err != nil {
		return err
	}
	if streamPayload {
		if err := writeAll(output, []byte(payloadParts[0])); err != nil {
			return err
		}
		file, openErr := os.Open(compressedName)
		if openErr != nil {
			return openErr
		}
		encodeErr := writeDiffBase85(output, file, info.Size())
		closeErr := file.Close()
		if encodeErr != nil {
			return encodeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := writeAll(output, []byte(payloadParts[1])); err != nil {
			return err
		}
	} else if err := writeAll(output, []byte(envelope)); err != nil {
		return err
	}
	if err := writeAll(output, []byte(staticParts[1])); err != nil {
		return err
	}
	return output.Flush()
}

func diskDiffStaticTemplate(encrypted bool) (string, error) {
	runtime, err := diffReportRuntime()
	if err != nil {
		return "", err
	}
	themeCSS, themeControl, err := diffReportAppearance()
	if err != nil {
		return "", err
	}
	browserJS, browserCSS, err := diffBrowserSupport()
	if err != nil {
		return "", err
	}
	footer := strings.Replace(diffReportFooterContent(encrypted), "HTML file: pending", diffStreamSizeMarker, 1)
	static := strings.NewReplacer("/* DIFF_FONTS */", embeddedFontCSS(), "/* DIFF_THEME_CSS */", themeCSS,
		"<!-- DIFF_THEME_SWITCH -->", themeControl, "/* NORMAL_BROWSER_CSS */", browserCSS,
		"/* DIFF_BROWSER_RUNTIME */", strings.Replace(diffBrowserRuntime, "/* NORMAL_BROWSER_FUNCTIONS */", browserJS, 1),
		"/* DIFF_RUNTIME */", runtime, "/* DIFF_PAYLOAD */", diffStreamPayloadMarker,
		"<!-- DIFF_FOOTER_CONTENT -->", footer, "DIFF_FAVICON", faviconDataURI()).Replace(diffReportTemplate)
	now := time.Now().Local()
	return replaceGeneratedMetadata(static, now.Format(time.RFC3339), now.Format("2006-01-02 15:04:05 MST")), nil
}

func writeDiskDiffJSON(w io.Writer, report *diskDiffReport) error {
	writeJSON := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return writeAll(w, data)
	}
	if _, err := io.WriteString(w, `{"before":`); err != nil {
		return err
	}
	if err := writeJSON(report.Before); err != nil {
		return err
	}
	if _, err := io.WriteString(w, `,"after":`); err != nil {
		return err
	}
	if err := writeJSON(report.After); err != nil {
		return err
	}
	if _, err := io.WriteString(w, `,"root":`); err != nil {
		return err
	}
	if err := writeDiskDiffTree(w, report); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"complete", report.Complete}, {"increased", report.Increased}, {"decreased", report.Decreased},
		{"statusCounts", report.StatusCounts}, {"rankings", report.Rankings},
	} {
		if err := writeStringAll(w, fmt.Sprintf(`,"%s":`, field.name)); err != nil {
			return err
		}
		if err := writeJSON(field.value); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `}`)
	return err
}

func writeDiskDiffTree(w io.Writer, report *diskDiffReport) error {
	f, err := os.Open(report.RecordsPath)
	if err != nil {
		return fmt.Errorf("open disk diff records: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), diffStreamRecordLimit)
	read := func() (*diskDiffRecord, error) {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("read disk diff record: %w", err)
			}
			return nil, nil
		}
		var rec diskDiffRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("invalid disk diff record: %w", err)
		}
		return &rec, nil
	}
	current, err := read()
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("disk diff records are missing the root")
	}
	if current.Depth != 0 {
		return fmt.Errorf("disk diff root depth is %d, want 0", current.Depth)
	}
	if current.Depth > diffStreamRendererDepthLimit {
		return fmt.Errorf("disk diff record depth exceeds %d", diffStreamRendererDepthLimit)
	}
	var parents []int
	var emitted []bool
	var count int64
	for current != nil {
		if current.Depth < 0 || current.Depth > diffStreamRendererDepthLimit || current.Depth > len(parents) {
			return fmt.Errorf("invalid disk diff record depth %d", current.Depth)
		}
		if current.Depth < len(parents) {
			return fmt.Errorf("invalid disk diff record depth transition to %d", current.Depth)
		}
		next, readErr := read()
		if readErr != nil {
			return readErr
		}
		if next != nil && (next.Depth < 0 || next.Depth > diffStreamRendererDepthLimit || next.Depth > current.Depth+1 || next.Depth == 0) {
			return fmt.Errorf("invalid disk diff record depth transition %d to %d", current.Depth, next.Depth)
		}
		if current.Depth > 0 {
			parent := current.Depth - 1
			if parent >= len(emitted) {
				return fmt.Errorf("invalid disk diff parent depth %d", parent)
			}
			if emitted[parent] {
				if _, err := io.WriteString(w, `,`); err != nil {
					return err
				}
			}
			emitted[parent] = true
		}
		node := current.Node
		node.Children = nil
		encoded, marshalErr := json.Marshal(node)
		if marshalErr != nil {
			return marshalErr
		}
		if len(encoded) > diffStreamRecordLimit {
			return fmt.Errorf("disk diff node exceeds the %d-byte record limit", diffStreamRecordLimit)
		}
		if len(encoded) == 0 || encoded[len(encoded)-1] != '}' {
			return fmt.Errorf("invalid serialized disk diff node")
		}
		if err := writeAll(w, encoded[:len(encoded)-1]); err != nil {
			return err
		}
		if next != nil && next.Depth == current.Depth+1 {
			if _, err := io.WriteString(w, `,"children":[`); err != nil {
				return err
			}
			parents = append(parents, current.Depth)
			emitted = append(emitted, false)
		} else {
			if _, err := io.WriteString(w, `}`); err != nil {
				return err
			}
			for len(parents) > 0 && parents[len(parents)-1] >= nextDepth(next) {
				parents = parents[:len(parents)-1]
				emitted = emitted[:len(emitted)-1]
				if _, err := io.WriteString(w, `]}`); err != nil {
					return err
				}
			}
		}
		count++
		current = next
	}
	for len(parents) > 0 {
		parents = parents[:len(parents)-1]
		emitted = emitted[:len(emitted)-1]
		if _, err := io.WriteString(w, `]}`); err != nil {
			return err
		}
	}
	if count != report.Count {
		return fmt.Errorf("disk diff record count mismatch: got %d, want %d", count, report.Count)
	}
	return nil
}

func nextDepth(record *diskDiffRecord) int {
	if record == nil {
		return -1
	}
	return record.Depth
}

func writeDiffBase85(w io.Writer, r io.Reader, length int64) error {
	buf := make([]byte, 32*1024)
	var carry []byte
	var block [4]byte
	var encoded [5]byte
	writeBlocks := func(data []byte) error {
		for len(data) >= 4 {
			copy(block[:], data[:4])
			value := uint32(block[0])<<24 | uint32(block[1])<<16 | uint32(block[2])<<8 | uint32(block[3])
			for i := 4; i >= 0; i-- {
				encoded[i] = base85Alphabet[value%85]
				value /= 85
			}
			if err := writeAll(w, encoded[:]); err != nil {
				return err
			}
			data = data[4:]
		}
		carry = append(carry[:0], data...)
		return nil
	}
	var consumed int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			consumed += int64(n)
			data := append(carry, buf[:n]...)
			if writeErr := writeBlocks(data[:len(data)/4*4]); writeErr != nil {
				return writeErr
			}
			if rem := len(data) % 4; rem > 0 {
				carry = append(carry[:0], data[len(data)-rem:]...)
			} else {
				carry = carry[:0]
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if consumed != length {
		return fmt.Errorf("compressed diff report length changed while encoding")
	}
	if len(carry) > 0 {
		for i := range block {
			block[i] = 0
		}
		copy(block[:], carry)
		value := uint32(block[0])<<24 | uint32(block[1])<<16 | uint32(block[2])<<8 | uint32(block[3])
		for i := 4; i >= 0; i-- {
			encoded[i] = base85Alphabet[value%85]
			value /= 85
		}
		return writeAll(w, encoded[:])
	}
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func writeStringAll(w io.Writer, value string) error {
	return writeAll(w, []byte(value))
}

type diffStreamLimitWriter struct {
	w              io.Writer
	limit, written int64
}

func (w *diffStreamLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.written {
		return 0, fmt.Errorf("compressed diff report exceeds the %d GiB streaming limit", w.limit>>30)
	}
	n, err := w.w.Write(p)
	w.written += int64(n)
	return n, err
}
