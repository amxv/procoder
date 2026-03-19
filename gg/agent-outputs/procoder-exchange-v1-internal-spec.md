# procoder Exchange V1 Internal Spec

## Purpose

This document translates the agreed product spec into an implementation-oriented internal spec.

It uses the final naming model:

- `procoder prepare` creates a task package
- `./procoder-return` creates a return package
- `procoder apply` applies a return package
- both packages are tied together by a stable exchange ID

## Canonical Terminology

- `exchange`
  One full round trip: local prepare, remote work, remote return, local apply.

- `exchange ID`
  Stable identifier for one exchange. It ties the task package and return package together.

- `task package`
  The zip created by `procoder prepare`, uploaded to ChatGPT.

- `return package`
  The zip created by `./procoder-return`, downloaded from ChatGPT and consumed by `procoder apply`.

- `task branch`
  The root branch created by `prepare` for the exchange, typically `refs/heads/procoder/<exchange-id>`.

- `task branch family`
  The writable ref scope for the exchange:
  - `refs/heads/procoder/<exchange-id>`
  - `refs/heads/procoder/<exchange-id>/*`

## Data Model

There are only two semantic JSON documents in V1.

### 1. `exchange.json`

`exchange.json` is the canonical exchange record.

It is created by `prepare`. It defines the contract for the exchange and does not change afterward.

It answers these questions:

- what exchange is this
- what refs and history were exported for context
- what task branch family is allowed to come back
- what commit is the base of the writable task branch family

It exists in two places, but it is the same document and schema in both:

- local copy:
  `.git/procoder/exchanges/<exchange-id>/exchange.json`
- exported repo copy:
  `.git/procoder/exchange.json`

The local copy exists for bookkeeping and diagnostics. The exported copy exists so `./procoder-return` can operate offline.

There is no separate `task.json`.

### 2. `return.json`

`return.json` is the generated return record inside the return package.

It is created by `./procoder-return`. It describes only what the remote side produced relative to the exchange.

It answers these questions:

- which exchange this return belongs to
- which task-family refs changed
- what their expected old OIDs were
- what their new OIDs are
- which bundle file contains the new objects

Suggested filename inside the return package:

- `procoder-return.json`

There is no separate "inbound manifest" concept. `return.json` is the return record.

## Internal Layout

### Local Repository

Internal machine-owned state lives under:

```text
.git/procoder/
  exchanges/
    <exchange-id>/
      exchange.json
```

The task package itself is user-facing and should default to the repo root:

```text
./procoder-task-<exchange-id>.zip
```

This exact filename should be added to `.git/info/exclude`.

### Exported Repository Inside The Task Package

Suggested layout:

```text
<repo-root>/
  ...tracked files...
  .git/
    procoder/
      exchange.json
  procoder-return
```

The helper binary and the generated return package should both be excluded through `.git/info/exclude` in the exported repo.

### Return Package

Suggested layout:

```text
procoder-return-<exchange-id>.zip
  procoder-return.json
  procoder-return.bundle
```

## `exchange.json` Schema

The exact field list may evolve, but V1 should include the following concepts:

```json
{
  "protocol": "procoder-exchange/v1",
  "exchange_id": "20260320-abc123",
  "created_at": "2026-03-20T10:30:00Z",
  "tool_version": "0.1.0",
  "source": {
    "head_ref": "refs/heads/main",
    "head_oid": "abc123..."
  },
  "task": {
    "root_ref": "refs/heads/procoder/20260320-abc123",
    "ref_prefix": "refs/heads/procoder/20260320-abc123",
    "base_oid": "abc123..."
  },
  "context": {
    "heads": {
      "refs/heads/main": "abc123...",
      "refs/heads/feature/x": "def456..."
    },
    "tags": {
      "refs/tags/v1.2.0": "fedcba..."
    }
  }
}
```

### Notes

- `task.root_ref` is the exact root task branch.
- `task.ref_prefix` defines the writable family.
- `task.base_oid` is the ancestor every returned task-family ref must descend from.
- `context.heads` and `context.tags` are the exported baseline snapshots used by `./procoder-return` to detect out-of-scope mutations.

## `procoder-return.json` Schema

Suggested V1 shape:

```json
{
  "protocol": "procoder-return/v1",
  "exchange_id": "20260320-abc123",
  "created_at": "2026-03-20T11:15:00Z",
  "tool_version": "0.1.0",
  "bundle_file": "procoder-return.bundle",
  "task": {
    "root_ref": "refs/heads/procoder/20260320-abc123",
    "base_oid": "abc123..."
  },
  "updates": [
    {
      "ref": "refs/heads/procoder/20260320-abc123",
      "old_oid": "abc123...",
      "new_oid": "def456..."
    },
    {
      "ref": "refs/heads/procoder/20260320-abc123/experiment",
      "old_oid": "",
      "new_oid": "7890ab..."
    }
  ]
}
```

### Notes

- `old_oid` is empty for a newly created task-family ref.
- create vs update is derived from whether `old_oid` is empty.
- the return package does not need to repeat the full context snapshot from `exchange.json`.

## Ref Rules

### Exported For Context

`prepare` exports:

- all local branch refs under `refs/heads/*`
- all local tags under `refs/tags/*`

These are available for read-only context in the exported repo.

### Allowed To Return

Only task-family refs may be returned by default:

- `refs/heads/procoder/<exchange-id>`
- `refs/heads/procoder/<exchange-id>/*`

The helper should fail if other branch refs or any tag refs changed from the baseline snapshot.

### Allowed History Shape

Every returned task-family ref must be a descendant of `task.base_oid`.

This guarantees:

- predictable bundle prerequisites
- simpler apply validation
- no unrelated history rewrite hidden inside the return package

## `prepare` Implementation

### Inputs

- current local Git repository
- current `HEAD`
- current clean working tree

### Preconditions

`prepare` must fail if any of the following are true:

- repository is not a Git worktree
- working tree is dirty
- index is dirty
- untracked files are present
- submodules are present
- Git LFS is detected

### Suggested Validation Commands

- clean worktree:
  `git status --porcelain=v1 --untracked-files=all`
- current head ref:
  `git symbolic-ref --quiet HEAD`
- current head OID:
  `git rev-parse HEAD`
- baseline heads:
  `git for-each-ref --format='%(refname) %(objectname)' refs/heads`
- baseline tags:
  `git for-each-ref --format='%(refname) %(objectname)' refs/tags`
- submodule detection:
  `git ls-files --stage`
  and reject mode `160000`

### Exchange ID

V1 can use a simple sortable ID such as:

`YYYYMMDD-HHMMSS-<short-random>`

Example:

`20260320-113015-a1b2c3`

### Prepare Flow

1. Resolve repo root and Git dir.
2. Validate preconditions.
3. Read current `HEAD` ref and OID.
4. Generate the exchange ID.
5. Create local task branch:
   `refs/heads/procoder/<exchange-id>` at current `HEAD`.
6. Build `exchange.json`.
7. Write local copy to:
   `.git/procoder/exchanges/<exchange-id>/exchange.json`
8. Create a temp export repo with `git init`.
9. Fetch all local heads from the source repo into the temp repo.
10. Fetch all tags from the source repo into the temp repo.
11. Configure commit identity in the temp repo.
12. Disable GPG signing in the temp repo.
13. Check out the task branch in the temp repo.
14. Write `.git/procoder/exchange.json` in the temp repo.
15. Copy in the `procoder-return` helper binary at repo root.
16. Add helper and generated zip patterns to `.git/info/exclude` in the temp repo.
17. Zip the temp repo to:
    `./procoder-task-<exchange-id>.zip`
18. Add the exact task-package filename to the source repo `.git/info/exclude`.
19. Print the task branch and absolute path.

### Commit Identity In Exported Repo

Use this precedence:

1. source repo local config `user.name` / `user.email`, if set
2. source repo global config, if visible
3. fallback defaults such as:
   - `user.name=procoder`
   - `user.email=procoder@local`

Always set:

- `commit.gpgsign=false`

## `procoder-return` Implementation

### Error UX Requirement

`procoder-return` is expected to be run by a remote coding agent, often without a human debugging loop.

For that reason, its failure messages are part of the product contract, not an implementation detail.

Every failure from `procoder-return` must:

- include a stable error code
- state the exact problem in plain language
- name the affected ref, file, or path when applicable
- include a short remediation hint that tells the agent what to do next
- avoid vague text such as "failed", "invalid state", or "could not continue" without specifics

When there is a concrete retry path, the hint should end with an instruction to rerun `./procoder-return`.

### Preconditions

The helper must fail if:

- it is not running inside a valid exported repo
- `.git/procoder/exchange.json` is missing or invalid
- the working tree is dirty
- there are no new commits on the task branch family
- refs outside the task branch family changed

### Helper Flow

1. Resolve repo root and Git dir.
2. Load `.git/procoder/exchange.json`.
3. Verify the working tree is clean.
4. Read current heads and tags.
5. Compare current heads and tags against the baseline in `exchange.json`.
6. Reject any changed branch ref outside the task branch family.
7. Reject any changed tag ref.
8. Compute changed task-family refs.
9. Fail with `NO_NEW_COMMITS` if nothing changed.
10. For each changed task-family ref, verify:
    `git merge-base --is-ancestor <task.base_oid> <new_oid>`
11. Build `procoder-return.json`.
12. Create `procoder-return.bundle`.
13. Zip both files into:
    `./procoder-return-<exchange-id>.zip`
14. Add the exact return-package filename to `.git/info/exclude`.
15. Print the absolute path and a `sandbox:` path hint.

### Required `procoder-return` Failure Quality

The helper should prefer precise failures over permissive behavior.

Required examples:

- dirty worktree:
  say that uncommitted or untracked changes were found, print a short summary, and instruct the agent to commit or discard them and rerun `./procoder-return`
- no new commits:
  say that no new commits were found on the task branch family and instruct the agent to create at least one commit before rerunning
- out-of-scope branch changes:
  name the offending refs and explain that only `refs/heads/procoder/<exchange-id>` and its children may be returned
- tag changes:
  name the changed tags and explain that V1 return packages do not support tag changes
- invalid exchange metadata:
  name the missing or unreadable path, such as `.git/procoder/exchange.json`, and explain that the helper must be run from a prepared task package
- non-descendant task branch:
  name the offending ref and explain that returned refs must descend from the task base commit

### Bundle Creation Strategy

Because every returned ref must descend from `task.base_oid`, the helper can build a bundle using `task.base_oid` as the prerequisite for each changed returned ref.

Conceptually:

- updated root task branch:
  `<task.base_oid>..<task.root_ref>`
- new child task branch:
  `<task.base_oid>..<child_ref>`

The bundle should advertise only the changed task-family refs.

## `apply` Implementation

### Inputs

- current local repository
- a return package path

### Output Behavior

By default:

- update task-family refs when safe
- fail clearly when not safe
- leave the current checkout unchanged

Optional flags:

- `--dry-run`
- `--namespace <prefix>`
- `--checkout`

### Preconditions

`apply` should fail if:

- the return package is invalid
- `procoder-return.json` is missing or invalid
- bundle verification fails
- fetched ref tips do not match `procoder-return.json`
- a target ref update is unsafe
- a target ref is currently checked out

### Apply Flow

1. Unzip the return package into a temp dir.
2. Read `procoder-return.json`.
3. Run `git bundle verify` on `procoder-return.bundle`.
4. Fetch the bundle into a temporary internal namespace such as:
   `refs/procoder/import/<nonce>/...`
5. Resolve the fetched OIDs and verify they match `procoder-return.json`.
6. Compute the destination refs.
7. Build an apply plan.
8. If `--dry-run` is set, print the plan and exit.
9. Ensure no destination ref to be updated is currently checked out.
10. Apply ref changes atomically with `git update-ref --stdin`.
11. If `--checkout` is set, check out the updated root task branch destination.
12. Clean up temporary import refs.

## Destination Ref Mapping

### Default Mapping

Without `--namespace`, each returned ref maps to itself.

Examples:

- `refs/heads/procoder/<exchange-id>` stays the same
- `refs/heads/procoder/<exchange-id>/experiment` stays the same

### Namespace Mapping

With `--namespace <prefix>`, map the returned task branch family under:

- `refs/heads/<prefix>/<exchange-id>`
- `refs/heads/<prefix>/<exchange-id>/*`

Examples:

- source:
  `refs/heads/procoder/20260320-abc123`
  becomes:
  `refs/heads/procoder-import/20260320-abc123`

- source:
  `refs/heads/procoder/20260320-abc123/experiment`
  becomes:
  `refs/heads/procoder-import/20260320-abc123/experiment`

## Safe Update Rules

### Root Task Ref Update

If `old_oid` is non-empty, the destination ref must still equal `old_oid`.

If not, fail with `BRANCH_MOVED` and suggest `--namespace`.

### New Task Ref Create

If `old_oid` is empty, the destination ref must not already exist.

If it exists, fail with `REF_EXISTS` and suggest `--namespace`.

### Checked-Out Ref Protection

If the destination ref is currently checked out in the main worktree, fail with `TARGET_BRANCH_CHECKED_OUT`.

This avoids moving a checked-out branch ref without updating the working tree.

## Error Model

Errors should have:

- a stable machine code
- a clear human message
- a short remediation hint when applicable

General output guidelines:

- print errors to stderr
- keep the first line concise and machine-scannable
- place details on following lines
- include concrete values such as branch names, expected OIDs, current OIDs, or file paths
- when there is a likely recovery path, include a `Hint:` line
- avoid stack traces or low-level command dumps by default
- preserve the underlying Git stderr internally for diagnostics, but translate it into higher-signal user-facing language

Suggested V1 codes:

- `NOT_GIT_REPO`
- `WORKTREE_DIRTY`
- `UNTRACKED_FILES_PRESENT`
- `SUBMODULES_UNSUPPORTED`
- `LFS_UNSUPPORTED`
- `INVALID_EXCHANGE`
- `INVALID_RETURN_PACKAGE`
- `BUNDLE_VERIFY_FAILED`
- `REF_OUT_OF_SCOPE`
- `NO_NEW_COMMITS`
- `BRANCH_MOVED`
- `REF_EXISTS`
- `TARGET_BRANCH_CHECKED_OUT`

### Example Error

```text
BRANCH_MOVED: cannot update refs/heads/procoder/20260320-abc123
Expected old OID: abc123...
Current local OID: f00baa...
Hint: rerun with --namespace procoder-import
```

### Example `procoder-return` Errors

```text
WORKTREE_DIRTY: repository has uncommitted or untracked changes
Found:
  M internal/app/app.go
  ?? scratch.txt
Hint: commit or discard these changes, then rerun ./procoder-return
```

```text
NO_NEW_COMMITS: no new commits found in the task branch family
Task branch family: refs/heads/procoder/20260320-abc123
Hint: create at least one commit on the task branch, then rerun ./procoder-return
```

```text
REF_OUT_OF_SCOPE: changed refs are outside the allowed task branch family
Changed refs:
  refs/heads/main
Allowed refs:
  refs/heads/procoder/20260320-abc123
  refs/heads/procoder/20260320-abc123/*
Hint: move your commits onto the task branch family, then rerun ./procoder-return
```

```text
INVALID_EXCHANGE: missing exchange metadata
Path: .git/procoder/exchange.json
Hint: run ./procoder-return only inside a repository created by procoder prepare
```

## Code Organization

Recommended packages:

- `internal/gitx`
  Git command execution, parsing, and typed errors.

- `internal/exchange`
  Shared types and helpers for `exchange.json`, `procoder-return.json`, exchange IDs, ref-family rules, and mapping logic.

- `internal/prepare`
  Clean export creation and task-package generation.

- `internal/returnpkg`
  Remote helper logic used by `cmd/procoder-return`.

- `internal/apply`
  Return-package verification, import planning, and atomic ref updates.

Entry points:

- `cmd/procoder/main.go`
- `cmd/procoder-return/main.go`

## Packaging Requirements

The installed local `procoder` CLI must have access to a prebuilt `procoder-return` helper binary for `linux/amd64`.

Release artifacts should therefore include:

- the host `procoder` binary for normal installation targets
- `procoder-return_linux_amd64`

Installer behavior:

- download both when available
- if falling back to local Go build, also build:
  `GOOS=linux GOARCH=amd64` for `procoder-return`

`prepare` should copy the already-installed helper binary into the exported repo. It should not compile the helper at prepare time.

## Test Strategy

The test suite should rely heavily on temp Git repos and integration-style scenarios.

Must-cover cases:

- `prepare` on a clean repo with multiple branches and tags
- `prepare` rejects dirty repos, untracked files, submodules, and LFS
- exported repo contains all heads and tags plus `procoder-return`
- `procoder-return` exports a normal update on the task branch
- `procoder-return` rejects out-of-scope branch mutations
- `procoder-return` rejects tag mutations
- `apply --dry-run` prints the correct plan
- `apply` updates the task branch successfully
- `apply` fails with `BRANCH_MOVED` and suggests `--namespace`
- `apply --namespace` imports successfully under remapped refs
- `apply` rejects updates to a checked-out destination ref

## Summary

The implementation hinges on one clean semantic split:

- `exchange.json` defines the exchange
- `procoder-return.json` reports the returned result

Everything else flows from that:

- `prepare` creates the task branch, local exchange record, exported repo, and task package
- `procoder-return` validates the exported repo state and creates the return package
- `apply` verifies the return package, imports the bundle into a temp namespace, and atomically updates destination refs

This keeps the data model small, the semantics clear, and the Git behavior rigorous without exposing unnecessary complexity in the user-facing CLI.
