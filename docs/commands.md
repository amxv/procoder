# Command Reference

This page keeps the command-level details out of the main README.

## `procoder prepare`

Run from the root of a clean Git worktree:

```bash
procoder prepare
```

What it does:

- validates that the repo is clean
- rejects untracked files, submodules, and Git LFS usage
- creates a local task branch at `refs/heads/procoder/<exchange-id>/task`
- writes the local exchange record to:
  `.git/procoder/exchanges/<exchange-id>/exchange.json`
- builds a sanitized export repo with local heads and tags for context
- injects the `procoder-return` helper at the exported repo root
- writes `./procoder-task-<exchange-id>.zip` at the source repo root

Notes:

- the current checkout is left unchanged
- on success, the command prints the task branch ref and the absolute task package path

## `procoder-return`

Run inside the prepared task package after ChatGPT has committed its work:

```bash
./procoder-return
```

What it does:

- verifies that it is running inside a prepared task package
- rejects dirty worktrees
- rejects tag changes
- rejects branch changes outside `refs/heads/procoder/<exchange-id>/*`
- verifies that returned refs descend from the prepared base commit
- writes `./procoder-return-<exchange-id>.zip` at the repo root

On success it prints:

- the absolute return package path
- a `sandbox:` hint that can be pasted back to the local user

Return package contents:

```text
procoder-return-<exchange-id>.zip
  procoder-return.json
  procoder-return.bundle
```

## `procoder apply`

Apply a return package from the original source repo:

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

Default behavior is:

- update task-family refs when safe
- otherwise fail clearly

If the prepared task branch moved locally, `procoder apply` fails with `BRANCH_MOVED` and suggests retrying with `--namespace`.

## `procoder --version`

Print the installed CLI version:

```bash
procoder --version
```

## Command Summary

```bash
procoder --help
procoder --version
procoder prepare
procoder apply <return-package.zip>
procoder apply <return-package.zip> --dry-run
procoder apply <return-package.zip> --namespace <prefix>
procoder apply <return-package.zip> --checkout
```

## Common Failure Shapes

`procoder` is designed to fail with clear, actionable messages.

Typical cases:

- the source repo is dirty when running `procoder prepare`
- ChatGPT changed refs outside the allowed task branch family before running `./procoder-return`
- the return package has no new commits
- the local task branch moved before `procoder apply`
- a destination ref already exists when using a namespace import

For the product-level rationale and internal workflow, see [How It Works](how-it-works.md).
