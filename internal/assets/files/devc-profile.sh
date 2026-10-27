# shellcheck shell=bash
#
# Installed by devc in /etc/profile.d: export the session environment and run
# claude with the user's settings and instructions taking precedence over the
# project's.

if [ -r /opt/devc/env ]; then
  # shellcheck source=/dev/null
  . /opt/devc/env
fi

[ -n "${BASH_VERSION:-}" ] || return 0

function claude() {
  # Remote Control server mode refuses --settings.
  if [[ "${1:-}" == "remote-control" ]]; then
    command claude "$@"
    return
  fi
  command claude --settings /opt/devc/claude-settings.json \
    --append-system-prompt-file /opt/devc/claude-prompt.md "$@"
}
