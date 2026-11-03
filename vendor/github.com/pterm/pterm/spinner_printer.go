package pterm

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/pterm/pterm/internal"
)

var activeSpinnerPrinters []*SpinnerPrinter

// DefaultSpinner is the default SpinnerPrinter.
var DefaultSpinner = SpinnerPrinter{
	Sequence:            []string{"▀ ", " ▀", " ▄", "▄ "},
	Style:               &ThemeDefault.SpinnerStyle,
	Delay:               time.Millisecond * 200,
	ShowTimer:           true,
	TimerRoundingFactor: time.Second,
	TimerStyle:          &ThemeDefault.TimerStyle,
	MessageStyle:        &ThemeDefault.SpinnerTextStyle,
	InfoPrinter:         &Info,
	SuccessPrinter:      &Success,
	FailPrinter:         &Error,
	WarningPrinter:      &Warning,
	Writer:              os.Stderr,
}

// SpinnerPrinter is a loading animation, which can be used if the progress is unknown.
// It's an animation loop, which can have a text and supports throwing errors or warnings.
// A TextPrinter is used to display all outputs, after the SpinnerPrinter is done.
type SpinnerPrinter struct {
	Text                string
	Sequence            []string
	Style               *Style
	Delay               time.Duration
	MessageStyle        *Style
	InfoPrinter         TextPrinter
	SuccessPrinter      TextPrinter
	FailPrinter         TextPrinter
	WarningPrinter      TextPrinter
	RemoveWhenDone      bool
	ShowTimer           bool
	TimerRoundingFactor time.Duration
	TimerStyle          *Style

	IsActive bool

	startedAt       time.Time
	currentSequence string

	Writer io.Writer
	// Runtime synchronization backported for race-free spinner updates/stops.
	mu       *sync.RWMutex
	outputMu *sync.Mutex
	wg       *sync.WaitGroup
	stopCh   chan struct{}
	stopOnce *sync.Once
}

// WithText adds a text to the SpinnerPrinter.
func (s SpinnerPrinter) WithText(text string) *SpinnerPrinter {
	s.Text = text
	return &s
}

// WithSequence adds a sequence to the SpinnerPrinter.
func (s SpinnerPrinter) WithSequence(sequence ...string) *SpinnerPrinter {
	s.Sequence = sequence
	return &s
}

// WithStyle adds a style to the SpinnerPrinter.
func (s SpinnerPrinter) WithStyle(style *Style) *SpinnerPrinter {
	s.Style = style
	return &s
}

// WithDelay adds a delay to the SpinnerPrinter.
func (s SpinnerPrinter) WithDelay(delay time.Duration) *SpinnerPrinter {
	s.Delay = delay
	return &s
}

// WithMessageStyle adds a style to the SpinnerPrinter message.
func (s SpinnerPrinter) WithMessageStyle(style *Style) *SpinnerPrinter {
	s.MessageStyle = style
	return &s
}

// WithRemoveWhenDone removes the SpinnerPrinter after it is done.
func (s SpinnerPrinter) WithRemoveWhenDone(b ...bool) *SpinnerPrinter {
	s.RemoveWhenDone = internal.WithBoolean(b)
	return &s
}

// WithShowTimer shows how long the spinner is running.
func (s SpinnerPrinter) WithShowTimer(b ...bool) *SpinnerPrinter {
	s.ShowTimer = internal.WithBoolean(b)
	return &s
}

// WithStartedAt sets the time when the SpinnerPrinter started.
func (s SpinnerPrinter) WithStartedAt(t time.Time) *SpinnerPrinter {
	s.startedAt = t
	return &s
}

// WithTimerRoundingFactor sets the rounding factor for the timer.
func (s SpinnerPrinter) WithTimerRoundingFactor(factor time.Duration) *SpinnerPrinter {
	s.TimerRoundingFactor = factor
	return &s
}

// WithTimerStyle adds a style to the SpinnerPrinter timer.
func (s SpinnerPrinter) WithTimerStyle(style *Style) *SpinnerPrinter {
	s.TimerStyle = style
	return &s
}

// WithWriter sets the custom Writer.
func (p SpinnerPrinter) WithWriter(writer io.Writer) *SpinnerPrinter {
	p.Writer = writer
	return &p
}

// SetWriter sets the custom Writer.
func (p *SpinnerPrinter) SetWriter(writer io.Writer) {
	p.Writer = writer
}

// ResetTimer resets the timer of the SpinnerPrinter.
func (s *SpinnerPrinter) ResetTimer() {
	s.startedAt = time.Now()
}

// SetStartedAt sets the time when the SpinnerPrinter started.
func (s *SpinnerPrinter) SetStartedAt(t time.Time) {
	s.startedAt = t
}

// UpdateText updates the message of the active SpinnerPrinter.
// Can be used live.
func (s *SpinnerPrinter) UpdateText(text string) {
	s.mu.Lock()
	s.Text = text
	style, messageStyle, writer, seq := s.Style, s.MessageStyle, s.Writer, s.currentSequence
	s.mu.Unlock()
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	if !RawOutput {
		Fprinto(writer, "\033[K"+style.Sprint(seq)+" "+messageStyle.Sprint(text))
	} else {
		Fprintln(writer, text)
	}
}

// Start the SpinnerPrinter.
func (s SpinnerPrinter) Start(text ...any) (*SpinnerPrinter, error) {
	s.mu = &sync.RWMutex{}
	s.outputMu = &sync.Mutex{}
	s.wg = &sync.WaitGroup{}
	s.stopCh = make(chan struct{})
	s.stopOnce = &sync.Once{}
	s.IsActive = true
	s.startedAt = time.Now()
	activeSpinnerPrinters = append(activeSpinnerPrinters, &s)

	if len(text) != 0 {
		s.Text = Sprint(text...)
	}

	sp := &s
	sp.wg.Add(1)
	go func() {
		defer sp.wg.Done()
		for {
			sp.mu.RLock()
			active, sequence := sp.IsActive, append([]string(nil), sp.Sequence...)
			sp.mu.RUnlock()
			if !active {
				return
			}
			for _, seq := range sequence {
				sp.mu.Lock()
				if !sp.IsActive {
					sp.mu.Unlock()
					return
				}
				style, messageStyle, timerStyle := sp.Style, sp.MessageStyle, sp.TimerStyle
				text, writer := sp.Text, sp.Writer
				showTimer, started, round, delay := sp.ShowTimer, sp.startedAt, sp.TimerRoundingFactor, sp.Delay
				sp.currentSequence = seq
				sp.mu.Unlock()
				if !RawOutput {
					timer := ""
					if showTimer {
						timer = " (" + time.Since(started).Round(round).String() + ")"
					}
					sp.outputMu.Lock()
					Fprinto(writer, style.Sprint(seq)+" "+messageStyle.Sprint(text)+timerStyle.Sprint(timer))
					sp.outputMu.Unlock()
				}
				select {
				case <-sp.stopCh:
					return
				case <-time.After(delay):
				}
			}
		}
	}()
	return sp, nil
}

// Stop terminates the SpinnerPrinter immediately.
// The SpinnerPrinter will not resolve into anything.
func (s *SpinnerPrinter) Stop() error {
	s.mu.Lock()
	if !s.IsActive {
		s.mu.Unlock()
		return nil
	}
	s.IsActive = false
	writer, remove := s.Writer, s.RemoveWhenDone
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	if RawOutput {
		return nil
	}
	if remove {
		fClearLine(writer)
		Fprinto(writer)
	} else {
		Fprintln(writer)
	}
	return nil
}

// GenericStart runs Start, but returns a LivePrinter.
// This is used for the interface LivePrinter.
// You most likely want to use Start instead of this in your program.
func (s *SpinnerPrinter) GenericStart() (*LivePrinter, error) {
	p2, _ := s.Start()
	lp := LivePrinter(p2)
	return &lp, nil
}

// GenericStop runs Stop, but returns a LivePrinter.
// This is used for the interface LivePrinter.
// You most likely want to use Stop instead of this in your program.
func (s *SpinnerPrinter) GenericStop() (*LivePrinter, error) {
	_ = s.Stop()
	lp := LivePrinter(s)
	return &lp, nil
}

// Info displays an info message
// If no message is given, the text of the SpinnerPrinter will be reused as the default message.
func (s *SpinnerPrinter) Info(message ...any) {
	if s.InfoPrinter == nil {
		s.InfoPrinter = &Info
	}

	if len(message) == 0 {
		message = []any{s.Text}
	}

	fClearLine(s.Writer)

	Fprinto(s.Writer, s.InfoPrinter.Sprint(message...))
	_ = s.Stop()
}

// Success displays the success printer.
// If no message is given, the text of the SpinnerPrinter will be reused as the default message.
func (s *SpinnerPrinter) Success(message ...any) {
	if s.SuccessPrinter == nil {
		s.SuccessPrinter = &Success
	}

	if len(message) == 0 {
		message = []any{s.Text}
	}

	fClearLine(s.Writer)

	Fprinto(s.Writer, s.SuccessPrinter.Sprint(message...))
	_ = s.Stop()
}

// Fail displays the fail printer.
// If no message is given, the text of the SpinnerPrinter will be reused as the default message.
func (s *SpinnerPrinter) Fail(message ...any) {
	if s.FailPrinter == nil {
		s.FailPrinter = &Error
	}

	if len(message) == 0 {
		message = []any{s.Text}
	}

	fClearLine(s.Writer)

	Fprinto(s.Writer, s.FailPrinter.Sprint(message...))
	_ = s.Stop()
}

// Warning displays the warning printer.
// If no message is given, the text of the SpinnerPrinter will be reused as the default message.
func (s *SpinnerPrinter) Warning(message ...any) {
	if s.WarningPrinter == nil {
		s.WarningPrinter = &Warning
	}

	if len(message) == 0 {
		message = []any{s.Text}
	}

	fClearLine(s.Writer)

	Fprinto(s.Writer, s.WarningPrinter.Sprint(message...))
	_ = s.Stop()
}
