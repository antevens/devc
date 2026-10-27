# devc Development Guide

## Commands

- Lint, test and build: `make`
- Build to `bin/devc`: `make build`
- Install to `~/.local/bin`: `make install`
- Test: `make test`
- Single test: `go test -v -run TestName ./internal/<package>`
- Format: `make fmt`
- Lint: `make lint`

## Layout

- `cmd/devc`: command dispatch, usage and bash completion template
- `internal/project`: workspace lifecycle, provisioning and sessions
- `internal/container`: container lookup and file extraction
- `internal/share`: shared home directory files and the change watcher
- `internal/claude`: Claude settings overlay and command lines
- `internal/forge`: forge detection, gh/glab installs, per-repository tokens
- `internal/clipboard`: OSC 52 filter and host clipboard relay
- `internal/terminal`: pseudo-terminal sessions on Linux
- `internal/release`: checksum verified release downloads
- `internal/config`: devc settings, embedded shared devcontainer config
- `internal/assets`: files installed in containers, under `files/`
- `internal/archive`: in-memory tar archives
- `internal/xdg`: XDG base directories

## Code Style

- `gofmt -s`, `go vet` and staticcheck clean
- Standard library only apart from fsnotify and golang.org/x/term
- Never modify a repository's devcontainer.json; add behaviour with
  devcontainer CLI flags or by copying files into the container
- Credentials that create tokens stay on the host; only per-repository
  tokens are copied into the container
- Errors to stderr prefixed `devc:`, normal output to stdout
- Shell scripts are files under `internal/assets/files`, embedded with
  `go:embed`, and follow the Google shell style guide: bash, `function`
  keyword definitions, lower case internal variables, upper case environment
  variables, `mapfile` for arrays, and they must pass shellcheck
