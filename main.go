package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const usageText = `Usage:
  webdiskstat [--input-type gdu|ncdu] [-o OUTPUT] [--password PASSWORD] [input]
  webdiskstat --diff [--complete] before.json[.gz] after.json[.gz] [-o OUTPUT]
  webdiskstat -v

Build a static WinDirStat-like web report from gdu or ncdu JSON.

Arguments:
  input                 Input JSON file, .gz file, or '-' for stdin. Defaults to stdin.

Options:
  --input-type FORMAT   Input JSON format: gdu or ncdu. Defaults to gdu.
  --diff                Compare two gdu snapshots and write a changes report.
  -c, --complete        Include unchanged nodes in a diff report (requires --diff).
  -o, --output PATH     HTML output path, or '-' for stdout. Defaults to webdiskstat.html.
  --password PASSWORD   Encrypt embedded report data with this password.
  -v, --version         Print the version and local build time.

Examples:
  gdu -o- / | webdiskstat -o webdiskstat.html
  ncdu -o- / | webdiskstat --input-type ncdu -o webdiskstat.html
  zcat report.json.gz | webdiskstat -o report.html
  webdiskstat --diff before.json after.json -o changes.html
  webdiskstat --diff --complete before.json after.json -o full-changes.html
`

type cliOptions struct {
	input     string
	inputType string
	output    string
	password  string
	hasPass   bool
	version   bool
	diff      bool
	complete  bool
	inputs    []string
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
	progress := newCLIProgress(stderr, os.Stderr, time.Now())
	defer progress.Close()
	opts, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 2
	}
	if opts.version {
		fmt.Fprintln(stdout, versionSummary())
		return 0
	}
	if opts.diff {
		return runDiffWithProgress(opts, stdin, stdout, stderr, progress)
	}
	if opts.hasPass && opts.password == "" {
		progress.Close()
		fmt.Fprintln(stderr, "webdiskstat: --password must not be empty")
		return 2
	}
	if opts.input == "-" && stdinIsTerminal(stdin) {
		progress.Close()
		printNoInputHelp(stderr)
		return 2
	}

	progress.Update("Reading scan data")
	raw, err := readJSON(opts.input, stdin)
	if err != nil {
		if errors.Is(err, errNoInput) {
			progress.Close()
			printNoInputHelp(stderr)
			return 2
		}
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	progress.Update("Preparing file tree")
	root, err := normalizeExport(raw, opts.inputType)
	if err != nil {
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}

	var password *string
	if opts.hasPass {
		password = &opts.password
	}
	progress.Update("Building HTML report")
	report, err := renderReport(root, password)
	if err != nil {
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	progress.Update("Writing report")
	outputPath, err := writeReport(report, opts.output, stdout)
	if err != nil {
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	dirs := max(0, root.Items-root.Files)
	if root.Type == "dir" {
		dirs++
	}
	if !progress.Finish(cliSummary{Path: outputPath, Bytes: len(report), Elapsed: time.Since(progress.started), Files: root.Files, Dirs: dirs, Encrypted: opts.hasPass}) && opts.output != "-" {
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
		case arg == "--diff":
			opts.diff = true
		case arg == "-c" || arg == "--complete":
			opts.complete = true
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
	if opts.complete && !opts.diff {
		return opts, fmt.Errorf("--complete requires --diff")
	}
	switch len(positional) {
	case 0:
	case 1:
		opts.input = positional[0]
	default:
		if !opts.diff || len(positional) != 2 {
			if opts.diff {
				return opts, fmt.Errorf("--diff requires exactly two input paths")
			}
			return opts, fmt.Errorf("expected at most one input path")
		}
	}
	if opts.diff {
		if len(positional) != 2 {
			return opts, fmt.Errorf("--diff requires exactly two input paths")
		}
		opts.inputs = positional
		if opts.inputType != "gdu" {
			return opts, fmt.Errorf("--diff supports gdu input only")
		}
		stdinCount := 0
		for _, source := range positional {
			if source == "-" {
				stdinCount++
			}
		}
		if stdinCount > 1 {
			return opts, fmt.Errorf("--diff accepts stdin ('-') at most once")
		}
	}
	return opts, nil
}

func runDiff(opts cliOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	p := newCLIProgress(stderr, os.Stderr, time.Now())
	defer p.Close()
	return runDiffWithProgress(opts, stdin, stdout, stderr, p)
}

func runDiffWithProgress(opts cliOptions, stdin io.Reader, stdout, stderr io.Writer, progress *cliProgress) int {
	if opts.hasPass && opts.password == "" {
		progress.Close()
		fmt.Fprintln(stderr, "webdiskstat: --password must not be empty")
		return 2
	}
	if len(opts.inputs) != 2 {
		progress.Close()
		fmt.Fprintln(stderr, "webdiskstat: --diff requires exactly two input paths")
		return 2
	}
	progress.Update("Loading and comparing snapshots")
	report, err := buildCompactDiffReport(opts.inputs[0], opts.inputs[1], stdin, opts.complete)
	if err != nil {
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	var password *string
	if opts.hasPass {
		password = &opts.password
	}
	progress.Update("Rendering and writing changes report")
	// Snapshot rows and the merge index are no longer reachable here. Reclaim
	// that completed phase before serialization allocates its short-lived nodes;
	// otherwise the old heap-growth target can exceed a container's memory cap.
	runtime.GC()
	outputPath, outputBytes, err := writeCompactDiffReport(opts.output, stdout, report, password)
	if err != nil {
		progress.Close()
		fmt.Fprintf(stderr, "webdiskstat: %v\n", err)
		return 1
	}
	if !progress.Finish(cliSummary{Path: outputPath, Bytes: int(outputBytes), Elapsed: time.Since(progress.started), Files: report.After.Files, Dirs: report.After.Directories, Diff: report.StatusCounts, Complete: report.Complete, Encrypted: opts.hasPass}) && opts.output != "-" {
		fmt.Fprintf(stderr, "Wrote %s\n", outputPath)
	}
	return 0
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
