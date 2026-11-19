package main

// console.go: the startup screen and live status in Shiron's window. Colors, the
// spinner and the window title are only used on a real terminal, so logs written to a
// file or collected by systemd stay plain. NO_COLOR=1 turns colors off too.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	outMu   sync.Mutex // one writer at a time: spinner, status lines and logs share the window
	isTTY   = isTerminal()
	colorOK = isTTY && os.Getenv("NO_COLOR") == "" && enableVT()
)

func isTerminal() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in an ANSI color when colors are on.
func paint(code, s string) string {
	if !colorOK {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

const (
	cBlurple = "38;5;105"
	cGreen   = "38;5;78"
	cYellow  = "38;5;221"
	cRed     = "38;5;203"
	cDim     = "2"
	cBold    = "1"
	cCyan    = "38;5;117"
)

func say(format string, a ...any) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Printf(format, a...)
}

// banner prints Shiron's name in a blurple-to-cyan gradient, with the version.
func banner(v release) {
	art := []string{
		` ███████╗██╗  ██╗██╗██████╗  ██████╗ ███╗   ██╗`,
		` ██╔════╝██║  ██║██║██╔══██╗██╔═══██╗████╗  ██║`,
		` ███████╗███████║██║██████╔╝██║   ██║██╔██╗ ██║`,
		` ╚════██║██╔══██║██║██╔══██╗██║   ██║██║╚██╗██║`,
		` ███████║██║  ██║██║██║  ██║╚██████╔╝██║ ╚████║`,
		` ╚══════╝╚═╝  ╚═╝╚═╝╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`,
	}
	gradient := []string{"38;5;105", "38;5;111", "38;5;117", "38;5;117", "38;5;123", "38;5;159"}
	say("\n")
	for i, line := range art {
		say("%s\n", paint(gradient[i], line))
	}
	say("  %s  %s\n\n", paint(cBold, "CS2 server tracker for Discord"), paint(cDim, v.Version+" · "+v.Date))
}

func stepOK(text string)   { say("  %s %s\n", paint(cGreen, "✔"), text) }
func stepWarn(text string) { say("  %s %s\n", paint(cYellow, "!"), text) }

// spin shows an animated spinner with text until done is called with the final line.
func spin(text string) (done func(ok bool, final string)) {
	if !isTTY {
		return func(ok bool, final string) {
			if ok {
				stepOK(final)
			} else {
				say("  ✖ %s\n", final)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		for i := 0; ; i++ {
			say("\r  %s %s", paint(cBlurple, frames[i%len(frames)]), text)
			select {
			case <-ctx.Done():
				return
			case <-time.After(80 * time.Millisecond):
			}
		}
	}()
	return func(ok bool, final string) {
		cancel()
		wg.Wait()
		mark := paint(cGreen, "✔")
		if !ok {
			mark = paint(cRed, "✖")
		}
		say("\r\x1b[2K  %s %s\n", mark, final)
	}
}

// panel draws a rounded box around lines; color is the border color.
func panel(color string, lines ...string) {
	width := 0
	for _, l := range lines {
		width = max(width, visibleLen(l))
	}
	say("  %s\n", paint(color, "╭"+strings.Repeat("─", width+2)+"╮"))
	for _, l := range lines {
		say("  %s %s%s %s\n", paint(color, "│"), l, strings.Repeat(" ", width-visibleLen(l)), paint(color, "│"))
	}
	say("  %s\n", paint(color, "╰"+strings.Repeat("─", width+2)+"╯"))
}

// visibleLen counts characters as shown, ignoring color codes.
func visibleLen(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case !inEsc:
			n++
		}
	}
	return n
}

// setTitle sets the console window's title (shown in the taskbar too).
func setTitle(title string) {
	if isTTY && colorOK {
		say("\x1b]0;%s\a", title)
	}
}

// ---------- live status ----------

type liveStatus struct {
	guilds, tracked, up, alerts int
}

var (
	lastShown     liveStatus
	statusPrinted bool
)

// reportStatus updates the window title every check and prints a status line when
// something changed. Only the poller calls it.
func reportStatus(st liveStatus) {
	down := st.tracked - st.up
	title := fmt.Sprintf("Shiron · ● Online · %s · %d CS2 servers (%d up) · %s",
		plural(st.guilds, "Discord server"), st.tracked, st.up, plural(st.alerts, "alert"))
	setTitle(title)
	if statusPrinted && st == lastShown {
		return
	}
	lastShown, statusPrinted = st, true
	health := paint(cGreen, fmt.Sprintf("%d up", st.up))
	if down > 0 {
		health += paint(cDim, " · ") + paint(cRed, fmt.Sprintf("%d down", down))
	}
	say("  %s %s %s %s %s %s %s %s\n",
		paint(cDim, time.Now().Format("15:04:05")), paint(cGreen, "●"),
		paint(cBold, plural(st.guilds, "Discord server")), paint(cDim, "·"),
		paint(cCyan, fmt.Sprintf("%d CS2 servers", st.tracked)), health,
		paint(cDim, "·"), plural(st.alerts, "alert"))
}

// guildEvent prints when Shiron is added to or removed from a Discord server.
func guildEvent(added bool, name string, total int) {
	mark, verb := paint(cGreen, "➕"), "Added to"
	if !added {
		mark, verb = paint(cYellow, "➖"), "Removed from"
	}
	say("  %s %s %s %s %s\n", paint(cDim, time.Now().Format("15:04:05")), mark, verb, paint(cBold, name),
		paint(cDim, fmt.Sprintf("(%s now)", plural(total, "Discord server"))))
}

// ---------- logs ----------

// logHandler prints log lines as "15:04:05 WARN message key=value", colored by level.
type logHandler struct {
	w     io.Writer
	attrs []slog.Attr
}

func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	level, color := "INFO", cDim
	switch {
	case r.Level >= slog.LevelError:
		level, color = "ERROR", cRed
	case r.Level >= slog.LevelWarn:
		level, color = "WARN", cYellow
	}
	var b strings.Builder
	b.WriteString("  " + paint(cDim, r.Time.Format("15:04:05")) + " " + paint(color, level) + " " + r.Message)
	add := func(a slog.Attr) { b.WriteString(" " + paint(cDim, a.Key+"=") + a.Value.String()) }
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	outMu.Lock()
	defer outMu.Unlock()
	_, err := fmt.Fprintln(h.w, b.String())
	return err
}

func (h *logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &logHandler{w: h.w, attrs: append(append([]slog.Attr(nil), h.attrs...), as...)}
}

func (h *logHandler) WithGroup(string) slog.Handler { return h }
