#!/bin/bash
#
# Append the last response of a Claude Code session to .claude.log in its
# working directory. Installed by devc as a Stop hook.

set -o errexit
set -o nounset
set -o pipefail

command -v jq >/dev/null || exit 0

input="$(cat)"
cwd="$(jq -r '.cwd // empty' <<<"${input}")"
response="$(jq -r '.last_assistant_message // empty' <<<"${input}")"
if [[ -z "${response}" ]]; then
  transcript="$(jq -r '.transcript_path // empty' <<<"${input}")"
  [[ -f "${transcript}" ]] || exit 0
  response="$(jq -rs '[.[] | select(.type == "assistant")] | last
    | .message.content[]? | select(.type == "text") | .text' \
    "${transcript}" 2>/dev/null)" || exit 0
fi
[[ -n "${cwd}" && -n "${response}" ]] || exit 0

printf '=== %s ===\n%s\n\n' "$(date -Iseconds)" "${response}" >>"${cwd}/.claude.log"
