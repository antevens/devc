#!/bin/bash
#
# Run by devc in the container: merge the kubeconfig of a cluster on the host
# into ~/.kube/config, keeping the current context when one is set.

set -o errexit
set -o nounset
set -o pipefail

#######################################
# Merge a kubeconfig fragment into ~/.kube/config.
# Arguments:
#   Path of the kubeconfig fragment.
#######################################
function main() {
  local fragment="$1"
  local config="${HOME}/.kube/config"
  local current=""
  local merged

  mkdir -p "${HOME}/.kube"
  if ! command -v kubectl >/dev/null; then
    # Merging needs kubectl; without it, only a missing config is filled.
    if [[ ! -e "${config}" ]]; then
      install -m 0600 "${fragment}" "${config}"
    fi
    return 0
  fi

  if [[ -e "${config}" ]]; then
    current="$(kubectl --kubeconfig "${config}" config current-context 2>/dev/null)" || current=""
  fi
  merged="$(mktemp "${HOME}/.kube/config.XXXXXX")"
  # The fragment is listed first so that its entries replace stale ones with
  # the same names from an earlier merge.
  if ! KUBECONFIG="${fragment}:${config}" kubectl config view --flatten >"${merged}"; then
    rm -f "${merged}"
    return 1
  fi
  chmod 0600 "${merged}"
  mv -f "${merged}" "${config}"
  if [[ -n "${current}" ]]; then
    kubectl --kubeconfig "${config}" config use-context "${current}" >/dev/null
  fi
}

main "$@"
