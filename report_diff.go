package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed assets/diff-report.html
var diffReportTemplate string

// Keep the comparison's palette and theme control identical to the ordinary
// optimized report, including refinements not present in example/report.html.
func diffReportAppearance() (string, string, error) {
	template := optimizedReportTemplate()
	var css strings.Builder
	for _, pattern := range []string{`:root\{[^}]+\}`, `html\[data-theme="light"\]\{[^}]+\}`} {
		rule := regexp.MustCompile(pattern).FindString(template)
		if rule == "" {
			return "", "", fmt.Errorf("diff report missing shared theme palette")
		}
		css.WriteString(rule + "\n")
	}
	// Copy only the current switcher rules, not legacy pill-switch styling or
	// unrelated ordinary-report layout. The reduced-motion rule stays scoped.
	for _, rule := range regexp.MustCompile(`[^{}]+\{[^{}]*\}`).FindAllString(template[strings.Index(template, "<style>"):strings.Index(template, "</style>")], -1) {
		selector := strings.TrimSpace(strings.SplitN(rule, "{", 2)[0])
		if selector == ".theme-toggle" || selector == ".theme-input" || selector == ".theme-icon" || strings.Contains(selector, "theme-switcher-v2") && !strings.Contains(rule, "transition:none") {
			css.WriteString(rule + "\n")
		}
	}
	control := regexp.MustCompile(`<label class="theme-toggle"[^>]*>.*?</label>`).FindString(template)
	if control == "" {
		return "", "", fmt.Errorf("diff report missing shared theme switcher")
	}
	return css.String(), control, nil
}

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
	themeCSS, themeControl, err := diffReportAppearance()
	if err != nil {
		return "", err
	}
	return strings.NewReplacer("/* DIFF_FONTS */", embeddedFontCSS(), "/* DIFF_THEME_CSS */", themeCSS, "<!-- DIFF_THEME_SWITCH -->", themeControl, "/* DIFF_RUNTIME */", runtime, "/* DIFF_PAYLOAD */", encoded, "DIFF_FAVICON", faviconDataURI()).Replace(diffReportTemplate), nil
}
