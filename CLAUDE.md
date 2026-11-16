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
- `internal/dockershim`: devc as the devcontainer CLI's docker, replacing
  home directory bind mounts with copies
- `internal/archive`: tar archives, in memory or streamed
- `internal/flock`: exclusive file locks between devc processes
- `internal/xdg`: XDG base directories

## Code Style

- `gofmt -s`, `go vet` and staticcheck clean
- Standard library only apart from fsnotify and golang.org/x/term
- Never modify a repository's devcontainer.json; add behaviour with
  devcontainer CLI flags or by copying files into the container
- Bind mounts of the home directory's hidden entries are replaced by copies
  (`internal/dockershim`); never create files on the host for a config's
  mounts
- Never copy files into a container mount; `Container.extract` refuses
  archives that would
- The host's `~/.bash_history` is only ever appended to, by devc, with
  validated lines; the container never gets access to it
- Credentials that create tokens stay on the host; only per-repository
  tokens are copied into the container
- Errors to stderr prefixed `devc:`, normal output to stdout
- Shell scripts are files under `internal/assets/files`, embedded with
  `go:embed`, and follow the Google shell style guide: bash, `function`
  keyword definitions, lower case internal variables, upper case environment
  variables, `mapfile` for arrays, and they must pass shellcheck
