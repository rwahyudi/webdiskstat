//go:build ignore

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const placeholderPayload = "{}"

func main() {
	data, err := os.ReadFile("example/report.html")
	if err != nil {
		panic(err)
	}
	template, err := stripPayload(string(data))
	if err != nil {
		panic(err)
	}
	output := "package main\n\n// Code generated from example/report.html; DO NOT EDIT.\nconst reportTemplate = " + strconv.Quote(template) + "\n"
	if err := os.WriteFile("template_data.go", []byte(output), 0o644); err != nil {
		panic(err)
	}
	fmt.Println("generated template_data.go")
}

func stripPayload(report string) (string, error) {
	const startMarker = "const REPORT_DATA_PAYLOAD = "
	const endMarker = ";\nlet DATA = null;"
	start := strings.Index(report, startMarker)
	if start < 0 {
		return "", fmt.Errorf("report template is missing payload marker")
	}
	payloadStart := start + len(startMarker)
	end := strings.Index(report[payloadStart:], endMarker)
	if end < 0 {
		return "", fmt.Errorf("report template is missing payload terminator")
	}
	payloadEnd := payloadStart + end
	var builder strings.Builder
	builder.Grow(len(report) - (payloadEnd - payloadStart) + len(placeholderPayload))
	builder.WriteString(report[:payloadStart])
	builder.WriteString(placeholderPayload)
	builder.WriteString(report[payloadEnd:])
	return builder.String(), nil
}
