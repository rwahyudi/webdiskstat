package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const usageText = `Usage:
  webdiskstat [--input-type gdu|ncdu] [-o OUTPUT] [--password PASSWORD] [input]
  webdiskstat -v

Build a static WinDirStat-like web report from gdu or ncdu JSON.

Arguments:
  input                 Input JSON file, .gz file, or '-' for stdin. Defaults to stdin.

Options:
  --input-type FORMAT   Input JSON format: gdu or ncdu. Defaults to gdu.
  -o, --output PATH     HTML output path, or '-' for stdout. Defaults to webdiskstat.html.
  --password PASSWORD   Encrypt embedded report data with this password.
  -v, --version         Print the version and local build time.

Examples:
  gdu -o- / | webdiskstat -o webdiskstat.html
  ncdu -o- / | webdiskstat --input-type ncdu -o webdiskstat.html
  zcat report.json.gz | webdiskstat -o report.html
`

type cliOptions struct {
	input     string
	inputType string
	output    string
	password  string
	hasPass   bool
	version   bool
}

var (
	version   = "dev"
	buildDate string
)

func main() {
	if code := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 2
	}
	if opts.version {
		fmt.Fprintln(stdout, versionSummary())
		return 0
	}
	if opts.hasPass && opts.password == "" {
		fmt.Fprintln(stderr, "webdiskstat: --password must not be empty")
		return 2
	}
	if opts.input == "-" && stdinIsTerminal(stdin) {
		printNoInputHelp(stderr)
		return 2
	}

	raw, err := readJSON(opts.input, stdin)
	if err != nil {
		if errors.Is(err, errNoInput) {
			printNoInputHelp(stderr)
			return 2
		}
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	root, err := normalizeExport(raw, opts.inputType)
	if err != nil {
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}

	var password *string
	if opts.hasPass {
		password = &opts.password
	}
	report, err := renderReport(root, password)
	if err != nil {
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	outputPath, err := writeReport(report, opts.output, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	if opts.output != "-" {
		fmt.Fprintf(stderr, "Wrote %s\n", outputPath)
	}
	return 0
}

func parseArgs(args []string, stderr io.Writer) (cliOptions, error) {
	opts := cliOptions{input: "-", inputType: "gdu", output: "webdiskstat.html"}
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			fmt.Fprint(stderr, usageText)
			return opts, flag.ErrHelp
		case arg == "-v" || arg == "--version":
			opts.version = true
		case arg == "-":
			positional = append(positional, arg)
		case arg == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case arg == "-o" || arg == "--output":
			value, next, err := optionValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.output = value
			i = next
		case strings.HasPrefix(arg, "--output="):
			opts.output = strings.TrimPrefix(arg, "--output=")
		case arg == "--input-type":
			value, next, err := optionValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.inputType = value
			i = next
		case strings.HasPrefix(arg, "--input-type="):
			opts.inputType = strings.TrimPrefix(arg, "--input-type=")
		case arg == "--password":
			value, next, err := optionValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.password = value
			opts.hasPass = true
			i = next
		case strings.HasPrefix(arg, "--password="):
			opts.password = strings.TrimPrefix(arg, "--password=")
			opts.hasPass = true
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown option: %s", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if opts.inputType != "gdu" && opts.inputType != "ncdu" {
		return opts, fmt.Errorf("unsupported input type: %s", opts.inputType)
	}
	switch len(positional) {
	case 0:
	case 1:
		opts.input = positional[0]
	default:
		return opts, fmt.Errorf("expected at most one input path")
	}
	return opts, nil
}

func versionSummary() string {
	date := buildDate
	if date == "" {
		if executable, err := os.Executable(); err == nil {
			if info, err := os.Stat(executable); err == nil {
				date = info.ModTime().Local().Format("2006-01-02 15:04:05 MST")
			}
		}
	}
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("%s %s\nBuilt: %s", appTitle, version, date)
}

func optionValue(args []string, index int, name string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return args[next], next, nil
}

func printNoInputHelp(stderr io.Writer) {
	fmt.Fprintln(stderr, "No input provided. Pipe gdu or ncdu JSON into the script or pass a saved JSON file.")
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, usageText)
}

func stdinIsTerminal(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func writeReport(report, output string, stdout io.Writer) (string, error) {
	if output == "-" {
		_, err := io.WriteString(stdout, report)
		return "<stdout>", err
	}
	path, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
