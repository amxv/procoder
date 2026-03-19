# procoder

`procoder` is a Go CLI for an offline Git exchange workflow between:

- a local developer repository
- a locked-down ChatGPT coding container

The default round trip is:

1. Run `procoder prepare` in a clean local repository.
2. Upload `./procoder-task-<exchange-id>.zip` to ChatGPT.
3. Work inside the exported repo, commit on the prepared task branch, and run `./procoder-return`.
4. Download `./procoder-return-<exchange-id>.zip`.
5. Run `procoder apply <return-package.zip>` in the source repository.

## Install

Global install via npm:

```bash
npm i -g procoder-cli
procoder --help
```

The installer fetches two release assets when available:

- the host `procoder` binary
- the packaged `linux/amd64` `procoder-return` helper used inside prepared task packages

If those release assets are unavailable, `scripts/postinstall.js` falls back to local Go builds for both binaries.

## Workflow

### `procoder prepare`

Run `procoder prepare` from the root of a clean Git worktree:

```bash
procoder prepare
```

`prepare` does the following:

- validates that the repo is clean and has no untracked files, submodules, or Git LFS usage
- creates a local task branch at `refs/heads/procoder/<exchange-id>/task`
- writes the local exchange record to `.git/procoder/exchanges/<exchange-id>/exchange.json`
- builds a sanitized export repo that includes local heads and tags for read-only context
- injects the `procoder-return` helper at the exported repo root
- writes `./procoder-task-<exchange-id>.zip` in the source repo root

The current checkout is left unchanged. On success the command prints the task branch ref and the absolute task package path.

### `./procoder-return`

After ChatGPT has made one or more commits inside the exported repo, run the helper from that prepared task package:

```bash
./procoder-return
```

The helper:

- verifies that it is running inside a prepared task package
- rejects dirty worktrees, tag changes, and branch changes outside `refs/heads/procoder/<exchange-id>/*`
- bundles only task-family refs that descend from the prepared base commit
- writes `./procoder-return-<exchange-id>.zip` at the repo root

On success it prints the absolute zip path and a `sandbox:` hint that can be pasted back to the local user.

### `procoder apply`

Apply the returned work from the original source repo:

```bash
procoder apply procoder-return-<exchange-id>.zip
```

Supported flags:

- `--dry-run`: verify the return package and print the ref update plan without mutating refs
- `--namespace <prefix>`: import returned refs under `refs/heads/<prefix>/<exchange-id>/...`
- `--checkout`: check out the updated default task branch after a successful apply

Examples:

```bash
procoder apply procoder-return-<exchange-id>.zip
procoder apply procoder-return-<exchange-id>.zip --dry-run
procoder apply procoder-return-<exchange-id>.zip --namespace procoder-import
procoder apply procoder-return-<exchange-id>.zip --checkout
```

Default apply behavior is "update when safe, otherwise fail." If the prepared task branch moved locally, `procoder apply` fails with a `BRANCH_MOVED` error and suggests retrying with `--namespace`.

## Command Summary

```bash
procoder --help
procoder prepare
procoder apply <return-package.zip>
procoder apply <return-package.zip> --dry-run
procoder apply <return-package.zip> --namespace <prefix>
procoder apply <return-package.zip> --checkout
procoder version
```

## Development

Common local commands:

```bash
make fmt
make test
make vet
make lint
make check
make build
make build-helper
make build-all
make install-local
```

`make build-all` produces the release binaries for the host CLI targets plus the packaged helper asset `procoder-return_linux_amd64`.

See [AGENTS.md](AGENTS.md) and [CONTRIBUTORS.md](CONTRIBUTORS.md) for implementation and release details.
