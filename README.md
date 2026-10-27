# devc

Create, start and attach to devcontainers for the current git repository, and run
a shell or Claude Code inside them.

Your shell history, git config, Claude Code settings, credentials, and
`~/.ai` preferences are shared with every container. When claude is started
by devc your Claude settings and instructions take precedence over the
project's.

## Requirements

- Docker
- The [devcontainer CLI](https://github.com/devcontainers/cli):
  `npm install -g @devcontainers/cli`
- `xclip`, `xsel` or `wl-clipboard` on the host to copy from containers to
  the clipboard
- Go 1.26 to build
- shellcheck for `make lint`

## Install

```sh
go install github.com/antevens/devc/cmd/devc@latest
source <(devc completion bash)  # add to ~/.bashrc
```

Or from a checkout, to `~/.local/bin`:

```sh
make install
```

## Usage

```text
devc up | start    Create and start the devcontainer
devc shell [cmd]   Open a login shell (or run cmd), starting the container if needed
devc claude [args] Run claude, starting the container if needed
devc remote [args] Run claude remote-control, so the Claude apps can start sessions
                   in the project
devc update        Recreate without image cache, picking up config changes and the
                   latest feature versions such as claude
devc rebuild       Recreate, reusing cached image layers
devc stop | down   Stop the devcontainer
devc rm [-y] [name|id...]
                   Remove the given devcontainers, or the workspace's after
                   confirmation, and revoke their repository tokens
devc init          Copy the shared config into .devcontainer/ to customise it
devc ls            List all devcontainers with their IDs and names
```

The workspace is the git top-level of the current directory. Outside a git
repository it is the nearest directory with a devcontainer config or
devcontainer, else the current directory. Repositories
with a `.devcontainer/devcontainer.json` or `.devcontainer.json` use it
unchanged. Others use the shared config at
`~/.config/devcontainer/devcontainer.json` (or `$DEVC_CONFIG`), created on
first use. A config copied by `devc init` is replaced by `devc update` with
the latest shared config, unless you have edited it.

## Removing devcontainers

`devc rm` without arguments shows the workspace's devcontainers and asks for
confirmation. It needs a terminal for the prompt, or `-y` to skip it.
`devc rm` with names or IDs, full or short as listed by `devc ls` or
`docker ps`, removes those devcontainers without asking. Containers that are
not devcontainers are refused.

For Docker Compose configs the other containers and the networks of the
Compose project are removed too. Named volumes are kept. `devc stop` stops
the whole Compose project.

## Tools in every devcontainer

`bash`, `vim` and `shellcheck` are installed in each devcontainer that lacks
them, with the container's package manager: apt, apk, dnf, microdnf, yum,
zypper or pacman.

## Compatibility with repository configs

devc never edits a repository's devcontainer.json. Everything it adds is
passed to the devcontainer CLI or copied into the running container:

- `--mount` binds `~/.bash_history` to `/mnt/devc/bash_history`, and sessions
  set `HISTFILE` to it. Mounts only apply when the container is created, so
  run `devc rebuild` for containers created without devc.
- `--additional-features` installs claude (and Node.js, which it needs) when
  the config has no claude-code feature. Features the config already has
  keep their options.
- Files are copied into the remote user's home directory, whichever user
  and image the config uses.

## Shared files

Copied into the container's home directory with symlinks followed, so
dotfiles kept in another directory work:

| Host | Notes |
| --- | --- |
| `~/.claude` | Settings, CLAUDE.md, agents, commands, skills, plugins and credentials. Session state such as `projects/`, `todos/` and `history.jsonl` stays per machine. |
| `~/.claude.json` | Account and MCP server config. |
| `~/.ai` | AI preferences. |
| `~/.gitconfig` | |
| `~/.docker/config.json` | Registries whose name contains `upload` are removed and the file is read-only, leaving pull access only. |
| `microk8s config` | Written to `~/.kube/config` when microk8s runs on the host. |

While a devc session runs, changes to `~/.claude` and `~/.ai`
on the host are copied into the container. Changes made in the container are
not copied back. Sync errors are logged to `~/.local/state/devc/devc.log`.

## Claude settings precedence

Claude Code ranks settings, highest first: managed, command line, local
project, shared project, user. Copied into the container, your
`~/.claude/settings.json` would rank below the project's, so devc also passes
it with `--settings`, which ranks above both project scopes. Your values win
where both set the same key. Lists such as permission rules and hooks are
merged from every scope, so the project's additions still apply.

CLAUDE.md files have no precedence order, so devc appends a system prompt
telling claude to prefer `~/.claude/CLAUDE.md` over the project's
instructions where they conflict.

Both flags are added by `devc claude` and by a `claude` shell function
installed in `/etc/profile.d` for login shells started with `devc shell`.

The settings passed this way also add a Stop hook that appends each claude
response to `.claude.log` in the working directory.

Sessions set `CLAUDE_CODE_SCROLL_SPEED`, the lines claude scrolls per mouse
wheel step in fullscreen mode, to 12 unless the container environment already
sets it.

## Clipboard

Programs in the container have no access to the host clipboard. They copy
with OSC 52 escape sequences instead: claude does when you select text or
run `/copy`, and so do tmux and neovim when configured to. devc runs sessions
on a pseudo-terminal of its own, removes these sequences from the output and
writes their text to both the host clipboard and the primary selection pasted
with the middle mouse button. It uses `xclip` or `xsel` on X11 and on Wayland
sessions with XWayland, and `wl-copy` on Wayland sessions without it, so
copying works in GNOME, KDE, XFCE and other desktops whether or not the
terminal supports OSC 52. Errors are logged to `~/.local/state/devc/devc.log`.

The container can write the clipboard but not read it. Paste into the
container with the terminal: Ctrl+Shift+V, or Shift and the middle mouse
button while claude has the mouse. Claude's own middle-click and image paste
read the clipboard and do not work.

Copying needs a Linux host with an X11 or Wayland display and a terminal on
both stdin and stdout. Otherwise the sequences pass through to the terminal
unchanged.

## devc config

Optional settings live in `~/.config/devc/config.json`:

```json
{
  "remote_control": true,
  "gitlab": [
    {"host": "gitlab.com", "token_command": "pass show gitlab/devc"}
  ],
  "github": [
    {"host": "github.com", "app_id": "123456", "private_key_file": "~/.config/devc/github-app.pem"}
  ]
}
```

## GitHub and GitLab

devc looks at the workspace's git remotes (origin first) and for `.github`,
`.gitlab-ci.yml` or `.gitlab`. For each forge found it installs the CLI, `gh`
or `glab`, in the container if missing, from the official release archive
verified against its published checksums and cached in `~/.cache/devc`.

With a credential configured for the remote's host, devc also gives the
container a token limited to that one repository. The credential that
creates these tokens is only used on the host and is never copied into the
container.

- **GitLab**: `token_command` prints a token with the `api` scope from a user
  with at least the Maintainer role on the project. devc creates a project
  access token with the Developer role and the `api` and `write_repository`
  scopes, valid for 30 days and replaced a week before it expires. It can
  push to unprotected branches and create, review and comment on merge
  requests. GitLab has no narrower merge request scope, so it can also make
  the project's other Developer level API calls, such as issues. Project
  access tokens may need a paid tier on gitlab.com.
- **GitHub**: GitHub has no API for creating personal access tokens, so the
  credential is a GitHub App. Create one with the repository permissions
  Contents and Pull requests set to read and write, install it on the
  repositories, and save its private key. devc mints installation tokens
  limited to the repository with those two permissions. They last an hour
  and are renewed while a session runs. Pushing changes to
  `.github/workflows` needs the Workflows permission, which is not requested.

In the container git uses the token through a credential helper, and SSH
remotes of the host are rewritten to HTTPS, as SSH keys are not shared. `glab`
gets `GITLAB_TOKEN` and `GITLAB_HOST` exported. `gh` reads the token from
`~/.config/gh/hosts.yml`: an exported variable would go stale when the token
is renewed. `devc rm` revokes the workspace's tokens.

## Claude apps

`devc remote` runs `claude remote-control` named after the workspace, so new
sessions in the project can be started from the Claude mobile app and
claude.ai/code while it runs. Server mode refuses `--settings`, so these
sessions use Claude's usual settings precedence.

With `"remote_control": true`, `devc claude` also makes each interactive
session available in the apps, named after the workspace. Sessions named
automatically use the workspace name as prefix, not the container's
hostname.

Remote Control requires claude to be logged in with a claude.ai
subscription. API keys and `claude setup-token` tokens are not supported.

## Development

```sh
make          # lint, test and build bin/devc
make test     # go test ./...
make lint     # gofmt, go vet, staticcheck, shellcheck, trailing whitespace
make fmt      # gofmt -s -w .
```

| Path | Contents |
| --- | --- |
| `cmd/devc` | Command line entry point and bash completion |
| `internal/project` | Workspace lifecycle: devcontainer CLI calls, provisioning, sessions |
| `internal/container` | Container lookup and file extraction through the docker CLI |
| `internal/share` | Files copied from the host home directory and the change watcher |
| `internal/claude` | Claude settings overlay and claude command lines |
| `internal/forge` | GitHub and GitLab detection, CLI installs and per-repository tokens |
| `internal/clipboard` | OSC 52 filter and host clipboard writes |
| `internal/terminal` | Pseudo-terminal sessions on Linux |
| `internal/release` | Checksum verified release downloads |
| `internal/config` | devc settings and the shared devcontainer config |
| `internal/assets` | Files installed in containers and their paths |
| `internal/archive` | In-memory tar archives |
| `internal/xdg` | XDG base directories |
