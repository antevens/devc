package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antevens/devc/internal/assets"
)

func TestHistoryLines(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		max     int
		want    string
		dropped int
	}{
		{"plain", "ls\ncd /tmp\n", 100, "ls\ncd /tmp\n", 0},
		{"timestamps and tabs", "#1700000000\nprintf 'a\tb'\n", 100, "#1700000000\nprintf 'a\tb'\n", 0},
		{"empty lines", "ls\n\n\npwd\n", 100, "ls\npwd\n", 0},
		{"escape sequence", "ls\necho \x1b]52;c;eA==\x07\npwd\n", 100, "ls\npwd\n", 1},
		{"carriage return", "rm -rf /\rls\n", 100, "", 1},
		{"C1 control", "echo \u009b31m\n", 100, "", 1},
		{"invalid UTF-8", "echo \xff\nls\n", 100, "ls\n", 1},
		{"utf-8", "echo héllo ✓\n", 100, "echo héllo ✓\n", 0},
		{"no final newline", "ls\npwd", 100, "ls\npwd\n", 0},
		{"over max", "aaaa\nbbbb\ncccc\n", 10, "aaaa\nbbbb\n", 1},
		{"cut at max", "aaaa\nbbbb\ncc", 11, "aaaa\nbbbb\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dropped := historyLines([]byte(tt.data), tt.max)
			if string(got) != tt.want || dropped != tt.dropped {
				t.Errorf("got %q, %d dropped, want %q, %d dropped", got, dropped, tt.want, tt.dropped)
			}
		})
	}
}

func TestAppendHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bash_history")

	if err := appendHistory(path, []byte("ls\n")); !os.IsNotExist(err) {
		t.Errorf("missing file: got %v, want a not-exist error", err)
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("missing file was created")
	}

	if err := os.WriteFile(path, []byte("one\ntwo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendHistory(path, []byte("three\n")); err != nil {
		t.Fatal(err)
	}
	if err := appendHistory(path, nil); err != nil {
		t.Fatal(err)
	}
	if err := appendHistory(path, []byte("four\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "one\ntwo\nthree\nfour\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}

	if err := appendHistory(dir, []byte("ls\n")); err == nil {
		t.Error("directory: want an error")
	}
}

// TestProfileHistorySeed runs the installed profile script in an interactive
// bash with rc settings like Ubuntu's, and checks that the seed is readable
// in the shell and that only the typed commands reach the history file.
func TestProfileHistorySeed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed")
	session := filepath.Join(dir, "session")
	profile := filepath.Join(dir, "profile.sh")
	rc := filepath.Join(dir, "rc")
	if err := os.WriteFile(seed, []byte("host-one\nhost-two\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, assets.Profile, 0o644); err != nil {
		t.Fatal(err)
	}
	rcText := ". " + profile + "\nHISTSIZE=1000\nHISTFILESIZE=2000\n" +
		`PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND$'\n'}history -a"` + "\n"
	if err := os.WriteFile(rc, []byte(rcText), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "--rcfile", rc, "-i")
	cmd.Env = append(os.Environ(), "HISTFILE="+session, "DEVC_HISTORY_SEED="+seed, "HOME="+dir)
	cmd.Stdin = strings.NewReader("echo typed\nhistory\nexit\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "host-two") {
		t.Errorf("seed not in the shell's history:\n%s", out)
	}
	got, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if want := "echo typed\nhistory\nexit\n"; string(got) != want {
		t.Errorf("history file got %q, want %q", got, want)
	}
	if data, _ := os.ReadFile(seed); string(data) != "host-one\nhost-two\n" {
		t.Errorf("seed changed: %q", data)
	}
}
