package main

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
	"text/template"
)

//go:embed completion.bash.tmpl
var completionTemplate string

var completionScript = template.Must(template.New("completion").Parse(completionTemplate))

// completion writes the completion script for the shell in args, bash by
// default.
func completion(w io.Writer, args []string) error {
	shell := "bash"
	if len(args) > 0 {
		shell = args[0]
	}
	if shell != "bash" {
		return fmt.Errorf("unsupported shell: %s", shell)
	}
	return completionScript.Execute(w, strings.Join(commands, " "))
}
