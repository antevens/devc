# devc Development Guide

## Commands
- Build: `make build`
- Install to ~/.local/bin: `make install`
- Test: `make test`, single test: `go test -v -run TestName .`
- Format: `make fmt`
- Lint: `make lint`

## Layout
- `main.go`: command dispatch and usage
- `project.go`: workspace, config selection, devcontainer CLI calls
- `container.go`: container inspection and file extraction
- `share.go`: files copied from the host home directory
- `claude.go`: Claude settings overlay, precedence prompt, hook and shell function
- `watch.go`: syncing host changes into a running session
- `config.go`: shared default config, `init` and `update` refresh
- `forge.go`: devc config, GitHub/GitLab detection and per-repository token cache
- `github.go`, `gitlab.go`: token creation and revocation for each forge
- `tools.go`: gh and glab installation from release archives
- `provision.go`: tokens, git credential helper, gh config and session launcher
- `devcontainer.json`: embedded default shared config

## Code Style
- Standard Go formatting, staticcheck clean
- Standard library only apart from fsnotify
- Never modify a repository's devcontainer.json; add behaviour with
  devcontainer CLI flags or by copying files into the container
- Credentials that create tokens stay on the host; only per-repository
  tokens are copied into the container
- Errors to stderr prefixed `devc:`, normal output to stdout
- Shell scripts: Google shell style guide, `function` keyword definitions,
  lower case internal variables, upper case environment variables, and
  they must pass shellcheck
