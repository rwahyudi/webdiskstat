package main

import (
	"strings"
	"testing"
)

func TestReportSecurityInfoDisclosure(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<details class="security-info" id="reportSecurityInfo">`,
		`<summary class="report-security plain" aria-controls="reportSecurityExplanation">`,
		`aria-labelledby="reportSecurityTitle"`,
		`aria-label="Close encryption information"`,
		`filenames, folder paths, sizes and available metadata`,
		`Anyone with the report can read this data.`,
		`It does not include the original files’ contents.`,
		`generate a new report with <code>--password</code>`,
		`This does not encrypt copies of this report you already have.`,
		`.security-info{position:relative;`,
		`event.key === "Escape"`,
		`document.addEventListener("pointerdown"`,
		`document.addEventListener("focusin"`,
		`info.addEventListener("keydown", event => event.stopPropagation());`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing security information %q", want)
		}
	}
	if strings.Contains(report, `title="Embedded scan data is not encrypted."`) {
		t.Error("plain security badge should use the disclosure, not a title-only explanation")
	}
}

func TestEncryptedSecurityBadgeHasNoPlainDataDisclosure(t *testing.T) {
	badge := securityStatusHTML(true)
	if !strings.Contains(badge, `class="report-security encrypted"`) || !strings.Contains(badge, `Data encrypted`) {
		t.Fatal("encrypted badge changed")
	}
	if strings.Contains(badge, "security-info") || strings.Contains(badge, "--password") {
		t.Fatal("encrypted badge must not include the plain-data explanation")
	}
	report := replaceSecurityStatus(reportTemplate, true)
	if strings.Contains(report, `id="reportSecurityInfo"`) || strings.Contains(report, `.security-info{`) {
		t.Fatal("encrypted report must not include plain-data disclosure or its styles")
	}
}
