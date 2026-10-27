#!/bin/bash
#
# Installed by devc: run a command with the session environment exported.

if [[ -r /opt/devc/env ]]; then
  # shellcheck source=/dev/null
  source /opt/devc/env
fi
exec "$@"
