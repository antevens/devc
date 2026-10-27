// Package clipboard writes text that programs in a container copy with OSC
// 52 escape sequences to the host's clipboard and primary selection.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// writeTimeout limits each run of a clipboard tool.
const writeTimeout = 5 * time.Second

// ErrNoDisplay is returned by FindHost when the host has no X11 or Wayland
// display.
var ErrNoDisplay = errors.New("no X11 or Wayland display")

// tool sets the clipboard and the primary selection, with one command each
// reading the text from stdin, on hosts where env is set.
type tool struct {
	env      string
	commands [][]string
}

// tools are tried in order. The X11 tools come first as they work on X11
// sessions and, through XWayland, on Wayland sessions.
var tools = []tool{
	{"DISPLAY", [][]string{{"xclip", "-selection", "clipboard"}, {"xclip", "-selection", "primary"}}},
	{"DISPLAY", [][]string{{"xsel", "--clipboard", "--input"}, {"xsel", "--primary", "--input"}}},
	{"WAYLAND_DISPLAY", [][]string{{"wl-copy"}, {"wl-copy", "--primary"}}},
}

// Host sets the host's clipboard and primary selection.
type Host struct {
	commands [][]string
}

// FindHost returns the first of tools installed for the host's display. It
// returns ErrNoDisplay when there is no display.
func FindHost() (*Host, error) {
	return findHost(os.Getenv, exec.LookPath)
}

func findHost(getenv func(string) string, lookPath func(string) (string, error)) (*Host, error) {
	display := false
	for _, t := range tools {
		if getenv(t.env) == "" {
			continue
		}
		display = true
		if _, err := lookPath(t.commands[0][0]); err == nil {
			return &Host{commands: t.commands}, nil
		}
	}
	if !display {
		return nil, ErrNoDisplay
	}
	return nil, errors.New("no xclip, xsel or wl-copy on the host")
}

// Set writes data to the clipboard and the primary selection. The tools
// keep serving the selections in the background, in sessions of their own
// so they outlive the terminal.
func (h *Host) Set(ctx context.Context, data []byte) error {
	var errs []error
	for _, args := range h.commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stdin = bytes.NewReader(data)
		detach(cmd)
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", strings.Join(args, " "), err))
		}
	}
	return errors.Join(errs...)
}

// Relay writes copies to a Host in the background. A copy replaces one
// still waiting to be written. Errors are logged.
type Relay struct {
	host *Host
	log  *slog.Logger
	next chan []byte
	done chan struct{}
}

// NewRelay starts relaying copies to host.
func NewRelay(host *Host, log *slog.Logger) *Relay {
	r := &Relay{host: host, log: log, next: make(chan []byte, 1), done: make(chan struct{})}
	go r.loop()
	return r
}

// Copy queues data for the host without waiting. It must not be called
// concurrently.
func (r *Relay) Copy(data []byte) {
	for {
		select {
		case r.next <- data:
			return
		default:
		}
		select {
		case <-r.next:
		default:
		}
	}
}

// Close writes the queued copy and stops the relay.
func (r *Relay) Close() {
	close(r.next)
	<-r.done
}

func (r *Relay) loop() {
	defer close(r.done)
	for data := range r.next {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		if err := r.host.Set(ctx, data); err != nil {
			r.log.Warn("clipboard", "err", err)
		}
		cancel()
	}
}
