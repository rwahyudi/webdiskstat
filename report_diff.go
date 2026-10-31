package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed assets/diff-report.html
var diffReportTemplate string

// diffReportRuntime shares the ordinary report's audited encoding and crypto,
// including its pure-JavaScript PBKDF2 fallback for offline file URLs.
func diffReportRuntime() (string, error) {
	template := optimizedReportTemplate()
	section := func(start, end string) (string, error) {
		i := strings.Index(template, start)
		if i < 0 {
			return "", fmt.Errorf("diff report runtime missing %s", start)
		}
		j := strings.Index(template[i:], end)
		if j < 0 {
			return "", fmt.Errorf("diff report runtime missing %s", end)
		}
		return template[i : i+j], nil
	}
	encoding, err := section("const BASE85_ALPHABET =", "function binaryReportReader(")
	if err != nil {
		return "", err
	}
	crypto, err := section("async function decryptReportPayload(", "const palette =")
	if err != nil {
		return "", err
	}
	crypto = strings.ReplaceAll(crypto, "await requestReportPassword()", "await requestReportPassword(promptText)")
	return encoding + crypto, nil
}

func renderDiffReport(report *DiffReport, password *string) (string, error) {
	if report == nil || report.Root == nil {
		return "", fmt.Errorf("diff report requires a root")
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if err := json.NewEncoder(writer).Encode(report); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	payload := map[string]any{"encrypted": false, "encoding": "base85", "length": compressed.Len(), "payload": base85Encode(compressed.Bytes())}
	if password != nil {
		var err error
		payload, err = encryptReportData(compressed.Bytes(), *password)
		if err != nil {
			return "", err
		}
	}
	payload["format"] = "gds-diff-json-v1"
	encoded, err := scriptJSON(payload)
	if err != nil {
		return "", err
	}
	runtime, err := diffReportRuntime()
	if err != nil {
		return "", err
	}
	return strings.NewReplacer("/* DIFF_FONTS */", embeddedFontCSS(), "/* DIFF_RUNTIME */", runtime, "/* DIFF_PAYLOAD */", encoded, "DIFF_FAVICON", faviconDataURI()).Replace(diffReportTemplate), nil
}
