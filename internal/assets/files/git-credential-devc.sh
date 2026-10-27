#!/bin/bash
#
# Installed by devc: git credential helper printing the per-repository token
# for the requested host, nothing for hosts without one.

set -o nounset

#######################################
# Print the credentials for the host in the request on stdin.
# Arguments:
#   Credential helper operation, only get is handled.
# Outputs:
#   Username and password lines for git, nothing without a token.
#######################################
function main() {
  local operation="${1:-}"
  local -a request
  local line
  local host=""
  local token_file

  [[ "${operation}" == "get" ]] || return 0

  mapfile -t request
  for line in "${request[@]}"; do
    if [[ "${line}" == host=* ]]; then
      host="${line#host=}"
    fi
  done

  token_file="/opt/devc/tokens/${host}"
  [[ -n "${host}" && -r "${token_file}" ]] || return 0

  printf 'username=x-access-token\npassword=%s\n' "$(<"${token_file}")"
}

main "$@"
