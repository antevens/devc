package terminal

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestRun(t *testing.T) {
	in, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer w.Close()

	var out bytes.Buffer
	cmd := exec.Command("sh", "-c", `test -t 0 && test -t 1 && test -t 2 && printf 'on a terminal'; exit 3`)
	err = Run(cmd, in, &out)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("err = %v, want exit status 3", err)
	}
	if out.String() != "on a terminal" {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunNotFound(t *testing.T) {
	in, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer w.Close()
	if err := Run(exec.Command("devc-test-missing-command"), in, &bytes.Buffer{}); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
