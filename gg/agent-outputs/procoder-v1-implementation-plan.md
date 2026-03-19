# procoder V1 Implementation Plan

## Purpose

This plan breaks procoder V1 into independently reviewable and testable phases.

Each phase implementer is expected to:

- read the required documents and files before coding
- stay within the phase scope
- add or update tests for the behavior introduced in that phase
- run the required validation commands
- DM the lead with:
  - a short summary
  - the files changed
  - the checks run
  - any open concerns or follow-up suggestions

The lead will review each phase, request fixes if needed, run checks again, and commit to `main` only after the phase is accepted.

## Global Required Reading For Every Phase

Every implementer must read these before touching code:

- `AGENTS.md`
- [`gg/agent-outputs/procoder-handoff-v1-product-spec.md`](./procoder-handoff-v1-product-spec.md)
- [`gg/agent-outputs/procoder-exchange-v1-internal-spec.md`](./procoder-exchange-v1-internal-spec.md)
- [`gg/agent-outputs/procoder-v1-implementation-plan.md`](./procoder-v1-implementation-plan.md)

## Review And Validation Policy

Rules that apply to every phase:

- prefer additive changes and do not revert unrelated work
- keep failures precise and helpful, especially for `procoder-return`
- add tests in the same phase as the behavior they protect
- favor integration tests for real Git behavior
- keep command help and README accurate for any user-visible behavior introduced in that phase

Minimum handoff back to the lead for every phase:

- changed files list
- commands run
- test results
- whether the phase is ready for review

## Phase 1: Shared Foundations

### Goal

Create the shared internal building blocks that later phases depend on:

- exchange types and JSON helpers
- Git command runner utilities
- stable error model and rendering helpers
- temporary test repo helpers for integration-style Git tests
- `procoder-return` entrypoint stub so later phases can wire packaging and injection against a real binary target

### Read Before Coding

- `cmd/procoder/main.go`
- `internal/app/app.go`
- `internal/app/app_test.go`
- `go.mod`
- `Makefile`
- `README.md`

### Files Later Phases Must Re-Read

Later phases must re-read every file created or heavily changed here, especially:

- any new files under `internal/exchange/`
- any new files under `internal/gitx/`
- any new files that define typed errors or output formatting
- any new test helpers or integration test fixtures
- `cmd/procoder-return/main.go`
- changes to `internal/app/app.go`

### Implementation Tasks

- create `internal/exchange` for exchange IDs, `exchange.json`, and `procoder-return.json` types and helpers
- create `internal/gitx` for running Git commands with structured stdout, stderr, and exit handling
- create a typed error model with stable codes, messages, and optional hints
- define shared output formatting helpers for user-facing failures
- add temp repo test helpers for creating and mutating Git repositories in tests
- add `cmd/procoder-return/main.go` as a real entrypoint target, even if its behavior is still minimal in this phase
- remove or isolate template-only assumptions that would block later commands

### Validation

- `go test ./...`
- add unit tests for exchange ID generation and JSON round-tripping
- add unit tests for typed error rendering
- add at least one integration-style test using a temp Git repo helper

### Reviewer Focus

- package boundaries are clean and not over-engineered
- typed errors are specific enough for later agent-facing use
- test helpers are good enough to support later phases instead of forcing ad hoc shell setup everywhere

## Phase 2: `procoder prepare`

### Goal

Implement `procoder prepare` end to end:

- preflight validation
- local task branch creation
- local exchange record writing
- clean export repo creation
- helper injection
- task package zip generation at repo root

Git-valid branch shape for this and later phases:

- default prepared task branch: `refs/heads/procoder/<exchange-id>/task`
- writable task-family prefix: `refs/heads/procoder/<exchange-id>`

### Read Before Coding

- all new shared packages from Phase 1
- `cmd/procoder/main.go`
- `internal/app/app.go`
- `internal/app/app_test.go`
- `README.md`
- `Makefile`

### Files Later Phases Must Re-Read

Later phases must re-read the actual prepare implementation, especially:

- new files under `internal/prepare/`
- changes to `internal/app/app.go`
- any package that resolves helper locations or manages internal `.git/procoder/` state
- any prepare integration tests

### Implementation Tasks

- add the `prepare` command path in the main CLI
- validate repository preconditions:
  - Git worktree
  - clean worktree
  - clean index
  - no untracked files
  - no submodules
  - no Git LFS
- generate the exchange ID
- create the local task branch without switching the current checkout
- write the local copy of `exchange.json` under `.git/procoder/exchanges/<id>/exchange.json`
- build the sanitized export repo using `git init` plus explicit fetches of local heads and tags
- configure commit identity and `commit.gpgsign=false` in the export repo
- copy in the `procoder-return` helper binary
- exclude helper and generated zip artifacts through `.git/info/exclude`
- write the exported copy of `exchange.json` to `.git/procoder/exchange.json`
- create `./procoder-task-<id>.zip` at the source repo root
- update CLI help text and tests for the new command

### Validation

- `go test ./...`
- add integration tests that verify:
  - task branch is created locally
  - current checkout is unchanged
  - local exchange record exists
  - task package zip is created at repo root
  - exported repo contains all local heads and tags
  - exported repo contains `.git/procoder/exchange.json`
  - exported repo contains `procoder-return`
- add failure tests for:
  - dirty repo
  - untracked files
  - submodules
  - LFS detection

### Reviewer Focus

- prepare output matches the spec exactly
- export repo is actually sanitized rather than copied wholesale
- validation failures are high-signal and actionable

## Phase 3: `procoder-return`

### Goal

Implement `./procoder-return` end to end:

- exported repo validation
- baseline ref comparison
- task-family enforcement
- descendant checks
- bundle generation
- return package zip generation

This phase must follow the Git-valid task-family shape established in the specs:

- default prepared task branch: `refs/heads/procoder/<exchange-id>/task`
- allowed returned refs: `refs/heads/procoder/<exchange-id>/*`

### Read Before Coding

- all shared packages from Phase 1
- all prepare code and tests from Phase 2
- `cmd/procoder-return/main.go`
- any helper resolution code added in Phase 2

### Files Later Phases Must Re-Read

Later phases must re-read:

- new files under `internal/returnpkg/`
- `cmd/procoder-return/main.go`
- any error-rendering or Git helper changes made here
- all `procoder-return` integration tests

### Implementation Tasks

- load and validate `.git/procoder/exchange.json`
- verify the worktree is clean before producing a return package
- compare current heads and tags against the baseline snapshots from `exchange.json`
- reject out-of-scope branch mutations
- reject tag mutations
- compute changed task-family refs
- fail with `NO_NEW_COMMITS` when nothing changed
- verify every changed task-family ref descends from `task.base_oid`
- generate `procoder-return.json`
- generate `procoder-return.bundle`
- create `./procoder-return-<id>.zip` at repo root
- print a clear absolute path and `sandbox:` hint on success
- ensure all `procoder-return` errors follow the explicit error UX requirement in the internal spec

### Validation

- `go test ./...`
- add integration tests for:
  - successful return package generation after real commits
  - dirty worktree failure
  - no new commits failure
  - out-of-scope branch mutation failure
  - tag mutation failure
  - non-descendant task ref failure
- assert error messages contain useful hints, not just error codes

### Reviewer Focus

- `procoder-return` errors are specific enough for autonomous recovery
- baseline comparison logic is correct and does not silently ignore out-of-scope changes
- bundle generation matches the declared return record

## Phase 4: `procoder apply --dry-run`

### Goal

Implement the read-only import path first:

- unzip and validate return package
- verify bundle
- fetch into a temp namespace
- compare fetched OIDs against `procoder-return.json`
- compute destination refs
- print a correct dry-run plan

This phase does not update local refs yet.

### Read Before Coding

- all shared packages from Phase 1
- all prepare code and tests from Phase 2
- all `procoder-return` code and tests from Phase 3
- `cmd/procoder/main.go`
- `internal/app/app.go`
- `README.md`

### Files Later Phases Must Re-Read

Later phases must re-read:

- new files under `internal/apply/`
- any dry-run output formatting helpers
- all apply dry-run integration tests

### Implementation Tasks

- add the `apply` command path in the main CLI
- read and unzip the return package
- validate the presence and shape of `procoder-return.json`
- run `git bundle verify`
- fetch the bundle into a temporary namespace such as `refs/procoder/import/<nonce>/...`
- verify fetched ref tips match the return record exactly
- compute default destination ref mapping
- compute `--namespace` destination ref mapping
- build a structured apply plan
- implement `--dry-run`
- print a user-facing plan that clearly shows creates, updates, conflicts, and namespace remaps

### Validation

- `go test ./...`
- add integration tests for:
  - successful dry-run against a real return package
  - invalid return package
  - invalid JSON
  - bundle verification failure
  - mismatched fetched OIDs versus return record
  - namespace mapping in the plan
  - branch-moved conflict detection in the plan

### Reviewer Focus

- dry-run is trustworthy enough to become the main inspection path
- temporary namespace handling is correct and cleaned up
- output is understandable by both humans and agents

## Phase 5: `procoder apply`

### Goal

Implement real ref updates and final apply behavior:

- atomic ref updates
- namespace import
- checked-out ref protection
- optional checkout after apply

### Read Before Coding

- all shared packages from Phase 1
- all prepare code and tests from Phase 2
- all `procoder-return` code and tests from Phase 3
- all apply dry-run code and tests from Phase 4
- `cmd/procoder/main.go`
- `internal/app/app.go`

### Files Later Phases Must Re-Read

Phase 6 must re-read:

- the final `internal/apply/` implementation
- final CLI help and user-facing output changes
- all apply integration tests

### Implementation Tasks

- turn the dry-run plan into real atomic ref updates using `git update-ref --stdin`
- implement safe update behavior for existing task-family refs
- implement create behavior for new task-family refs
- implement `--namespace <prefix>`
- implement checked-out destination ref protection
- implement `--checkout`
- keep the default behavior "update when safe, otherwise fail"
- ensure `BRANCH_MOVED`, `REF_EXISTS`, and `TARGET_BRANCH_CHECKED_OUT` failures include helpful hints

### Validation

- `go test ./...`
- add integration tests for:
  - successful apply of a normal return package
  - successful namespace import
  - branch moved failure with namespace hint
  - existing destination ref failure with namespace hint
  - checked-out destination ref failure
  - `--checkout` success path
- add at least one end-to-end test covering:
  - prepare
  - simulated remote work
  - procoder-return
  - apply

### Reviewer Focus

- ref updates are atomic and safe
- no hidden behavior mutates unexpected refs
- namespace remapping preserves the task branch family structure exactly

## Phase 6: Packaging, Release, Docs, And Regression Coverage

### Goal

Make the feature shippable:

- package the helper binary with the local CLI
- update release automation
- update installer fallback behavior
- update docs
- add final regression coverage

### Read Before Coding

- all code and tests from Phases 1 through 5
- `package.json`
- `scripts/postinstall.js`
- `bin/procoder.js`
- `.github/workflows/release.yml`
- `Makefile`
- `README.md`
- `CONTRIBUTORS.md`

### Files Later Phases Must Re-Read

This is the last planned phase. No later phase dependency list is required.

### Implementation Tasks

- make the installed CLI able to locate a packaged `procoder-return` helper binary for `linux/amd64`
- update installer logic to download or build both:
  - the host `procoder` binary
  - the `linux/amd64` `procoder-return` helper
- update release workflow to publish the helper asset
- update any build scripts or Make targets needed for helper builds
- update README command documentation for:
  - `procoder prepare`
  - `./procoder-return`
  - `procoder apply`
- add final regression tests or smoke coverage that protect the round trip

### Validation

- `go test ./...`
- `npm run test`
- `make check`
- verify the release workflow changes are internally consistent
- add or update tests for installer fallback logic where practical

### Reviewer Focus

- local install behavior matches release expectations
- helper asset handling works for both prebuilt and fallback build paths
- docs match the actual command behavior and filenames exactly
