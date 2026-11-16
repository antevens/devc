# shellcheck shell=bash
#
# Installed by devc in /etc/profile.d: export the session environment, load
# the host's bash history, and run claude with the user's settings and
# instructions taking precedence over the project's.

if [ -r /opt/devc/env ]; then
  # shellcheck source=/dev/null
  . /opt/devc/env
fi

[ -n "${BASH_VERSION:-}" ] || return 0

# DEVC_HISTORY_SEED is a copy of the host's ~/.bash_history. It is read at
# the first prompt: after the rc files have set HISTSIZE, and before any
# command is recorded, so `history -a` writes only commands typed here.
if [[ $- == *i* && -n "${DEVC_HISTORY_SEED:-}" && -r "${DEVC_HISTORY_SEED}" ]]; then
  #######################################
  # Read the host history seed once, then remove this hook.
  # Globals:
  #   PROMPT_COMMAND
  #   DEVC_HISTORY_SEED
  #######################################
  function __devc_history_seed() {
    PROMPT_COMMAND="${PROMPT_COMMAND#__devc_history_seed}"
    PROMPT_COMMAND="${PROMPT_COMMAND#$'\n'}"
    unset -f __devc_history_seed
    history -r "${DEVC_HISTORY_SEED}"
  }
  PROMPT_COMMAND="__devc_history_seed${PROMPT_COMMAND:+$'\n'${PROMPT_COMMAND}}"
fi

function claude() {
  # Remote Control server mode refuses --settings.
  if [[ "${1:-}" == "remote-control" ]]; then
    command claude "$@"
    return
  fi
  command claude --settings /opt/devc/claude-settings.json \
    --append-system-prompt-file /opt/devc/claude-prompt.md "$@"
}
