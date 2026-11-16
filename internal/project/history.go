package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/flock"
	"github.com/antevens/devc/internal/xdg"
)

// Bash history is shared with the host without mounting the host's
// ~/.bash_history. devc copies it into the container as a seed that shells
// read at their first prompt, and appends the commands typed in the container
// to it afterwards. The container has no access to the host file: devc only
// appends to it, and only lines it has validated.
const (
	// historyDir holds the history files in the container, relative to the
	// remote user's home directory.
	historyDir = ".local/state/devc"
	// maxHistoryMerge caps the bytes of container history one merge appends
	// to the host file.
	maxHistoryMerge = 1 << 20
)

// historySeed returns the container path of the copy of the host history.
func historySeed(c *container.Container) string {
	return path.Join(c.Home, historyDir, "history-seed")
}

// historyFile returns the container path of the history file of devc
// sessions, holding the commands typed since the last merge.
func historyFile(c *container.Container) string {
	return path.Join(c.Home, historyDir, "bash_history")
}

// historyEnv returns the devcontainer exec arguments pointing shells at the
// session history file and the host history seed.
func historyEnv(c *container.Container) []string {
	return []string{
		"--remote-env", "HISTFILE=" + historyFile(c),
		"--remote-env", "DEVC_HISTORY_SEED=" + historySeed(c),
	}
}

// syncHistory appends the container history not yet on the host to the
// host's ~/.bash_history, then copies the host history into the container as
// the seed for new shells.
func (p *Project) syncHistory(c *container.Container) error {
	if c.Overlaps(path.Join(c.Home, historyDir)) {
		return fmt.Errorf("not sharing bash history: %s overlaps a mount in the container", path.Join(c.Home, historyDir))
	}
	if err := p.mergeHistory(c); err != nil {
		return err
	}
	seed, err := os.ReadFile(p.hostHistory())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	a := archive.New(0, 0)
	a.AddDir(historyDir, 0o700)
	if seed != nil {
		a.AddFile(path.Join(historyDir, "history-seed"), seed, 0o400)
	}
	data, err := a.Bytes()
	if err != nil {
		return err
	}
	return c.ExtractHome(data)
}

// mergeHistory appends the commands typed in the container since the last
// merge to the host's ~/.bash_history. The container's history file is
// renamed before it is read, so running shells start a new one, and the
// renamed file is removed once its lines are on the host. Nothing is merged
// while the host has no history file; the container keeps its lines.
func (p *Project) mergeHistory(c *container.Container) error {
	unlock, err := flock.Lock(filepath.Join(xdg.StateDir(p.home), "history.lock"))
	if err != nil {
		return err
	}
	defer unlock()

	host := p.hostHistory()
	if _, err := os.Stat(host); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	pending := historyFile(c) + ".merging"
	script := `f="$1"; m="$2"
if [ ! -e "$m" ] && [ -e "$f" ]; then mv -f "$f" "$m"; fi
if [ -e "$m" ]; then head -c "$3" "$m"; fi`
	out, err := c.Output(c.User, "sh", "-c", script, "sh", historyFile(c), pending, strconv.Itoa(maxHistoryMerge+1))
	if err != nil {
		return fmt.Errorf("read the container's bash history: %w", err)
	}
	lines, dropped := historyLines(out, maxHistoryMerge)
	if err := appendHistory(host, lines); err != nil {
		return fmt.Errorf("append to %s: %w", host, err)
	}
	if _, err := c.Output(c.User, "rm", "-f", pending); err != nil {
		return fmt.Errorf("remove the merged container history: %w", err)
	}
	if dropped > 0 {
		return fmt.Errorf("left %d lines of the container's bash history out of %s: control characters, invalid UTF-8 or over %d bytes", dropped, host, maxHistoryMerge)
	}
	return nil
}

func (p *Project) hostHistory() string {
	return filepath.Join(p.home, ".bash_history")
}

// historyLines returns the lines of history data that are safe to append to
// the host history file, and the number of lines left out. Empty lines,
// lines with control characters other than tab, lines that are not valid
// UTF-8, and lines beyond max bytes are left out, as is a last line without
// a newline when data was cut at max.
func historyLines(data []byte, max int) ([]byte, int) {
	var out bytes.Buffer
	dropped := 0
	truncated := len(data) > max
	rest := data
	for len(rest) > 0 {
		line, after, found := bytes.Cut(rest, []byte("\n"))
		rest = after
		if len(line) == 0 {
			continue
		}
		if (!found && truncated) || out.Len()+len(line)+1 > max || !printable(line) {
			dropped++
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), dropped
}

// printable reports whether line is valid UTF-8 without control characters
// other than tab.
func printable(line []byte) bool {
	if !utf8.Valid(line) {
		return false
	}
	for _, r := range string(line) {
		if (r < 0x20 && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// appendHistory appends lines to the history file at path in one write,
// starting on a new line. It never creates, truncates or rewrites the file.
func appendHistory(path string, lines []byte) error {
	if len(lines) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if last[0] != '\n' {
			lines = append([]byte("\n"), lines...)
		}
	}
	_, err = f.Write(lines)
	return err
}
