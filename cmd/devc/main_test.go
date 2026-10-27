package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/antevens/devc/internal/container"
)

func TestCompletion(t *testing.T) {
	var buf bytes.Buffer
	if err := completion(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `compgen -W "`+strings.Join(commands, " ")+` --help --version"`) {
		t.Errorf("completion lacks the commands:\n%s", buf.String())
	}
	if err := completion(&buf, []string{"zsh"}); err == nil {
		t.Error("zsh: want an error")
	}
}

func TestReport(t *testing.T) {
	if got := report(nil); got != 0 {
		t.Errorf("nil: %d", got)
	}
	err := exec.Command("sh", "-c", "exit 3").Run()
	if got := report(err); got != 3 {
		t.Errorf("exit 3: %d", got)
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"--version"}} {
		if got := run(context.Background(), args); got != 0 {
			t.Errorf("run(%v) = %d", args, got)
		}
	}
}

func TestConfirm(t *testing.T) {
	targets := []container.Summary{{ID: "0123456789abcdef", Name: "repo-app-1"}}
	for answer, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "": false, "yep\n": false} {
		var out bytes.Buffer
		got, err := confirm(strings.NewReader(answer), &out, targets, "/src/repo")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("answer %q: got %v, want %v", answer, got, want)
		}
		if !strings.Contains(out.String(), "repo-app-1 (0123456789ab) for /src/repo") {
			t.Errorf("prompt lacks the devcontainer:\n%s", out.String())
		}
	}
}
