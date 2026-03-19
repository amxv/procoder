# procoder Exchange V1 Product Spec

## Status

Working product spec based on the current design discussion. This document captures the agreed CLI surface, workflow, protocol expectations, constraints, and reasoning. It intentionally stops short of implementation details.

## Canonical Terminology

- `exchange`
  One full round trip: local prepare, remote work, remote return, local apply.

- `exchange ID`
  Stable identifier that ties the task package and return package together.

- `task package`
  The zip created by `procoder prepare` and uploaded to ChatGPT.

- `return package`
  The zip created by `./procoder-return` and consumed by `procoder apply`.

## Problem

The goal of `procoder` is to support an offline coding exchange workflow between:

- a local developer repository
- a locked-down ChatGPT coding container with common developer tools but no normal internet access

The workflow should feel simple:

1. The local user runs `procoder prepare` in a repository.
2. The tool produces a task package and creates a dedicated task branch.
3. The user uploads that task package to ChatGPT and asks the model to do the work.
4. The model commits on the prepared branch and runs a bundled helper binary named `procoder-return`.
5. The helper produces a return package.
6. The local user runs `procoder apply <return-package.zip>`.
7. The returned commits are integrated into the local repository safely and with minimal friction.

## Product Goals

- Keep the common path extremely simple for both humans and agents.
- Preserve real Git commits and branches rather than reducing the workflow to plain diffs.
- Give the remote model broad repository context, including local branches and tags.
- Strip remotes, credentials, logs, reflogs, hooks, and other irrelevant local state from the export.
- Make the default apply path feel like the agent worked on a local task branch created by `prepare`.
- Provide clear failures and actionable hints so both humans and agents can recover.

## Non-Goals For V1

- No Git LFS support.
- No submodule support.
- No tag updates in the return/apply flow.
- No prompt generation bundled into the task package.
- No persistent `.procoder` directory committed into user repositories.
- No complicated policy surface with many public modes or flags.

## Target Remote Environment

The current ChatGPT container target is:

- Debian 13
- `x86_64`
- `linux/amd64`

The bundled helper should therefore be a small `linux/amd64` Go binary named `procoder-return`.

## End-User CLI Surface

### Local User

Primary commands:

- `procoder prepare`
- `procoder apply <return-package.zip>`
- `procoder apply <return-package.zip> --dry-run`

Secondary power-user flags:

- `procoder apply <return-package.zip> --namespace <prefix>`
- `procoder apply <return-package.zip> --checkout`

Notes:

- `--dry-run` is the main inspection path. There is no separate `inspect` command.
- `--namespace` is the escape hatch for rare cases where the user does not want returned refs applied to their original task branch names.
- `--checkout` is optional and should check out the updated task branch after a successful apply.

### Remote ChatGPT Agent

The agent-side experience should be minimal:

1. work in the exported repository
2. commit changes normally
3. run `./procoder-return`
4. return the generated zip path to the user as a `sandbox:` link

No subcommand should be required in the common path. The default behavior of `procoder-return` is to generate the return package.

## Default Workflow

### `procoder prepare`

Default behavior:

- validate that the source repository is clean
- create a dedicated task branch for the exchange
- do not switch the local working tree to that branch
- build a clean transport repository in a temporary location
- include all local branches and tags as read-only context
- strip remotes, credentials, hooks, logs, reflogs, and unrelated config
- inject the `procoder-return` helper binary
- configure the exported repo so commits work immediately:
  - set `user.name`
  - set `user.email`
  - disable `commit.gpgsign`
- check out the task branch in the exported repo
- produce the task package

The exported repository should be broad in context but narrow in what it is allowed to hand back.

### Remote Work

Default behavior:

- the exported repo is already on the prepared task branch
- the model does not need to create its own branch in the normal case
- the model can inspect old local branches and tags for context
- the model commits on the prepared task branch
- when finished, the model runs `./procoder-return`

### `procoder apply`

Default behavior:

- read and validate the return package
- verify the bundle prerequisites
- update the prepared task branch locally when it is safe to do so
- create any additional returned task-family branches when allowed and non-conflicting
- fail clearly if the target branch moved or if safety checks do not pass

The default should be "update when safe, otherwise fail."

This is the right default for the main workflow because `prepare` created the task branch specifically for the exchange. It should feel like the agent made commits on that branch locally.

## Branch Model

### Task Branch Family

Each exchange should have a dedicated branch family keyed by the exchange ID.

Example shape:

- `procoder/<exchange-id>`
- `procoder/<exchange-id>/...`

Rules:

- the writable branch family is restricted to the current exchange ID
- the helper should only export changes for that allowed branch family by default
- other branches and tags are available for read-only context

This separation is important:

- repository context can be broad
- returned mutable refs must stay narrow

## Task Package Contents

The task package should contain:

- a clean exported working tree
- a sanitized `.git` directory
- the `procoder-return` helper binary at the repo root
- machine-readable exchange metadata

Suggested layout:

```text
<repo-name>/
  ...tracked files...
  .git/
    procoder/
      exchange.json
  procoder-return
```

The task package should not contain:

- remote credentials
- remote definitions that are unnecessary for the exchange
- hooks
- reflogs
- working-tree junk
- ignored files
- untracked files
- prompt files

The default user-facing task package filename should be:

```text
./procoder-task-<exchange-id>.zip
```

## Return Package Contents

The return package should remain mechanical and small.

Suggested layout:

```text
procoder-return-<exchange-id>.zip
  procoder-return.json
  procoder-return.bundle
```

The return package should contain:

- the bundle with new Git objects and advertised refs
- a small machine-readable manifest describing intended ref updates

It should not require a human summary file in V1.

## Apply Policy

### Default Policy

The default policy is conceptually "update when safe."

In practice that means:

- if the prepared task branch still points to the expected base commit, update it to the returned tip
- if the branch moved locally, fail
- if the return package attempts to touch refs outside the allowed task branch family, fail unless a special-case override exists

### `--namespace`

`--namespace` is the special-case escape hatch.

Instead of updating the original task branch names, `apply` rewrites returned refs under a user-specified prefix.

This is useful when:

- the prepared task branch moved locally
- the user wants to preserve the returned work without touching the original branch names
- the user wants to review or merge manually afterward

### `--dry-run`

`--dry-run` should show:

- which refs would be created
- which refs would be updated
- which safety checks passed
- which safety checks would fail
- whether the user should retry with `--namespace`

### `--checkout`

After a successful apply, `--checkout` should switch the local repository to the updated task branch.

Default behavior without this flag should be to leave the current checkout unchanged and print what happened.

## Failure Behavior

Failures should be explicit, readable, and useful to both humans and agents.

### Apply Failure Examples

If the prepared task branch moved locally:

- explain that the branch no longer points to the expected base commit
- show the expected commit and current commit
- suggest `--namespace <prefix>` if the user wants to import the returned work under a new branch name instead

If the return package contains refs outside the allowed task branch family:

- explain which refs were rejected
- explain that V1 only allows the current exchange's task branch family by default

If bundle verification fails:

- explain that the local repo is missing required prerequisite objects or the return package is invalid

If the return package contains no new commits:

- explain that nothing new was detected and no apply was performed

### Agent-Side Failure Examples

If the exported repo is dirty when `./procoder-return` runs:

- explain that the agent must commit or discard local changes before producing the return package

If no new commits are found on the allowed task branch family:

- explain that there is nothing to export

If the helper cannot determine a valid exchange repo:

- explain that it must be run from a prepared task package

These errors should include short, concrete remediation hints rather than generic failures.

## Design Rationale

### Why `prepare` Does A Clean Export

This gives the main wins at once:

- smaller transfer size
- removal of ignored and untracked files
- removal of credentials and remote configuration
- reduction of noisy local Git state
- consistent exported environment for the remote agent

This also means a separate "clean export" command is unnecessary.

### Why All Local Branches And Tags Are Included

The remote model benefits from rich Git context:

- older branches
- branch history
- prior implementation paths
- tags that may mark releases or important snapshots

That context is valuable for real software work, even though only the current exchange branch family is allowed to come back as mutable output.

### Why The Default Apply Policy Is Update-When-Safe

This matches the intended common path:

- `prepare` created the task branch
- the remote agent worked on that branch
- `apply` should update that branch locally when safety checks pass

This keeps the tool simple for 99 percent of users.

### Why `--namespace` Exists

Branch movement and naming conflicts are important edge cases, but not the main path.

`--namespace` gives users and agents a clean recovery path without turning the normal UX into a policy-heavy interface.

### Why The Helper Is Named `procoder-return`

The helper is not a general agent CLI. It exists specifically to produce the return package from the prepared task package. The name should reflect that focused purpose.

## Deferred Internal Details

These details are intentionally deferred to the implementation discussion:

- exact exchange ID format
- exact default zip naming scheme
- exact manifest schema fields
- exact implementation strategy for building the clean export repo
- exact implementation strategy for packaging or cross-compiling the helper binary
- exact representation of tags inside the sanitized export
- exact low-level Git commands used by `prepare`, the helper, and `apply`

## Summary

The agreed V1 product shape is:

- `procoder prepare` creates a dedicated task branch, builds a clean export, and produces a task package
- the export includes all local branches and tags for context, but only the current exchange branch family is writable
- the remote agent commits and runs `./procoder-return`
- `procoder apply <return-package.zip>` updates the prepared task branch locally when safe
- `procoder apply <return-package.zip> --dry-run` shows the plan without changing anything
- `procoder apply <return-package.zip> --namespace <prefix>` is the main recovery path for conflicts or alternate import behavior
- `procoder apply <return-package.zip> --checkout` optionally checks out the updated task branch after apply

This keeps the workflow simple for both humans and agents while preserving the important Git semantics needed for real repository work.
