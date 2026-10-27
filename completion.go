package main

import (
	"fmt"
	"strings"
)

const bashCompletion = `# bash completion for devc, load with: source <(devc completion bash)

#######################################
# Complete devc arguments for bash programmable completion.
# Globals:
#   COMP_WORDS
#   COMP_CWORD
#   COMPREPLY
#######################################
function _devc_complete() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  if [[ "${COMP_CWORD}" -eq 1 ]]; then
    mapfile -t COMPREPLY < <(compgen -W "%s --help --version" -- "${cur}")
  elif [[ "${COMP_CWORD}" -eq 2 ]]; then
    case "${COMP_WORDS[1]}" in
      shell)
        mapfile -t COMPREPLY < <(compgen -c -- "${cur}")
        ;;
      completion)
        mapfile -t COMPREPLY < <(compgen -W "bash" -- "${cur}")
        ;;
      *) ;;
    esac
  fi
}

complete -F _devc_complete devc
`

// completion prints the completion script for shell, only bash is supported.
func completion(args []string) error {
	shell := "bash"
	if len(args) > 0 {
		shell = args[0]
	}
	if shell != "bash" {
		return fmt.Errorf("unsupported shell: %s", shell)
	}
	fmt.Printf(bashCompletion, strings.Join(commands, " "))
	return nil
}
