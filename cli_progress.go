package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/pterm/pterm"
	"golang.org/x/term"
)

type cliProgress struct {
	writer        io.Writer
	started       time.Time
	active, color bool
	stage         string
	spinner       *pterm.SpinnerPrinter
	closed        bool
	finished      bool
	width         int
}

func newCLIProgress(writer io.Writer, terminal *os.File, started time.Time) *cliProgress {
	p := &cliProgress{writer: writer, started: started}
	_, noColor := os.LookupEnv("NO_COLOR")
	p.active = terminal != nil && term.IsTerminal(int(terminal.Fd())) && os.Getenv("TERM") != "dumb" && os.Getenv("CI") == "" && writer == terminal
	p.color = p.active && !noColor
	if p.active {
		p.width, _, _ = term.GetSize(int(terminal.Fd()))
		if p.width < 20 {
			p.width = 80
		}
	}
	return p
}

func (p *cliProgress) Update(stage string) {
	if p == nil || !p.active {
		return
	}
	p.stage = cleanTerminalText(stage)
	if p.spinner == nil {
		sp := pterm.DefaultSpinner.WithWriter(p.writer).WithDelay(100 * time.Millisecond).WithShowTimer(true).WithTimerRoundingFactor(time.Second).WithRemoveWhenDone(true)
		sp.Style = pterm.NewStyle(pterm.FgCyan)
		sp.MessageStyle = pterm.NewStyle()
		sp.TimerStyle = pterm.NewStyle(pterm.FgGray)
		if !p.color {
			sp.Style = pterm.NewStyle()
			sp.TimerStyle = pterm.NewStyle()
		}
		p.spinner, _ = sp.Start(p.stage)
	} else {
		p.spinner.UpdateText(p.stage)
	}
}

type cliSummary struct {
	Path                string
	Bytes, Files, Dirs  int
	Elapsed             time.Duration
	Diff                map[string]int
	Complete, Encrypted bool
}

func (p *cliProgress) Finish(s cliSummary) bool {
	if p == nil || !p.active {
		return false
	}
	if p.finished {
		return true
	}
	p.Close()
	p.finished = true
	path := truncateTerminalText(cleanTerminalText(s.Path), p.width-4)
	title, label := "✓ Report ready", "HTML"
	if p.color {
		title = pterm.NewStyle(pterm.FgGreen, pterm.Bold).Sprint(title)
		label = pterm.NewStyle(pterm.FgCyan).Sprint(label)
	}
	fmt.Fprintf(p.writer, "\r\033[2K%s\n", title)
	writeSummaryRow(p.writer, p.width, fmt.Sprintf("  %s  %s", label, path))
	writeSummaryRow(p.writer, p.width, fmt.Sprintf("  Size  %s · %s", humanBytes(int64(s.Bytes)), s.Elapsed.Round(time.Millisecond)))
	writeSummaryRow(p.writer, p.width, fmt.Sprintf("  Tree  %s files · %s folders", comma(s.Files), comma(s.Dirs)))
	if s.Diff != nil {
		mode := "changed only"
		if s.Complete {
			mode = "complete"
		}
		writeSummaryRow(p.writer, p.width, fmt.Sprintf("  Diff  +%d added · −%d removed", s.Diff["added"], s.Diff["removed"]))
		writeSummaryRow(p.writer, p.width, fmt.Sprintf("        ↑%d grown · ↓%d shrunk · %d changed", s.Diff["grown"], s.Diff["shrunk"], s.Diff["changed"]))
		writeSummaryRow(p.writer, p.width, "        "+mode)
	}
	if s.Encrypted {
		writeSummaryRow(p.writer, p.width, "  Encrypted report")
	}
	return true
}

func (p *cliProgress) Close() {
	if p == nil || !p.active {
		return
	}
	if p.closed {
		return
	}
	p.closed = true
	if p.spinner != nil {
		_ = p.spinner.Stop()
	}
}

func truncateTerminalText(value string, width int) string {
	if width < 8 {
		width = 8
	}
	r := []rune(value)
	if len(r) <= width {
		return value
	}
	return string(r[:width-1]) + "…"
}

func writeSummaryRow(w io.Writer, width int, text string) {
	fmt.Fprintln(w, truncateTerminalText(text, width))
}

func cleanTerminalText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsControl(r) && r != '\u001b' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func comma(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return comma(n/1000) + fmt.Sprintf(",%03d", n%1000)
}
func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
