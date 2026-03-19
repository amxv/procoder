# AGENTS.md

Guidance for coding agents working in `procoder`.

## Purpose

This repo builds `procoder`, a Go CLI for an offline Git exchange workflow between:

- a local developer repository
- a locked-down ChatGPT coding container

The main user flow is:

- `procoder prepare`
- remote work inside the exported repo
- `./procoder-return`
- `procoder apply <return-package.zip>`

## Architecture

- `cmd/procoder/main.go`: process entrypoint, error handling, exits non-zero on failure.
- `cmd/procoder-return/main.go`: remote helper entrypoint used inside prepared task packages.
- `internal/app/app.go`: command parser + top-level CLI handlers.
- `internal/exchange/`: exchange IDs, task-ref helpers, and JSON models for `exchange.json` / `procoder-return.json`.
- `internal/gitx/`: Git command runner with structured output and typed failures.
- `internal/prepare/`: `procoder prepare` implementation.
- `internal/returnpkg/`: `./procoder-return` implementation.
- `internal/apply/`: `procoder apply` implementation.
- `bin/procoder.js`: npm shim that invokes packaged native binary.
- `scripts/postinstall.js`: downloads or builds both the host CLI binary and the packaged `linux/amd64` helper.
- `.github/workflows/release.yml`: tag-driven release pipeline.

## Exchange Model

The current Git-valid task-family shape is:

- default prepared task branch: `refs/heads/procoder/<exchange-id>/task`
- writable task-family prefix: `refs/heads/procoder/<exchange-id>`
- allowed returned refs: `refs/heads/procoder/<exchange-id>/*`

Important:

- do not reintroduce the invalid older shape where both `refs/heads/procoder/<exchange-id>` and `refs/heads/procoder/<exchange-id>/*` exist at the same time
- Git cannot store both of those refs because one path would need to be both a ref and a directory

Machine-owned metadata lives under `.git/procoder/`.

- local exchange record: `.git/procoder/exchanges/<exchange-id>/exchange.json`
- exported repo exchange record: `.git/procoder/exchange.json`

User-facing artifacts live at repo root by default:

- task package: `./procoder-task-<exchange-id>.zip`
- return package: `./procoder-return-<exchange-id>.zip`

When changing exchange behavior, keep `gg/agent-outputs/procoder-handoff-v1-product-spec.md` and `gg/agent-outputs/procoder-exchange-v1-internal-spec.md` aligned with the code.

## Local commands

Use `make` targets:

- `make fmt`
- `make test`
- `make vet`
- `make lint`
- `make check`
- `make build`
- `make build-helper`
- `make build-all`
- `make install-local`

Direct commands:

- `go test ./...`
- `go vet ./...`
- `npm run lint`

Phase-oriented validation:

- `procoder prepare` changes should be covered by integration tests under `internal/prepare/`
- `procoder-return` changes should be covered by integration tests under `internal/returnpkg/`
- `procoder apply` changes should be covered by integration tests under `internal/apply/`

## How to customize safely

1. Rename CLI command consistently in all places:
- directory `cmd/procoder`
- `package.json` values (`bin`, `config.cliBinaryName`)
- `bin/procoder.js`
- workflow env `CLI_BINARY`
- `Makefile` `BIN_NAME`

2. Keep binary naming convention unchanged unless you also update postinstall/workflow:
- release assets: `<cli>_<goos>_<goarch>[.exe]`
- packaged helper asset: `procoder-return_linux_amd64`
- npm-installed binary path: `bin/<cli>-bin` (or `.exe` on Windows)

3. If adding dependencies, commit `go.sum` and optionally enable Go cache in workflow.

4. Keep help output expressive and command-local (`<command> --help` should explain examples).

5. If you change exchange filenames, helper asset names, or task-family ref naming, update:
- code
- tests
- `README.md`
- `AGENTS.md`
- both spec docs under `gg/agent-outputs/`

## Release contract

Release pipeline triggers on `v*` tags and expects:

- `NPM_TOKEN` GitHub secret present.
- npm package name in `package.json` is publishable under your account/org.
- repository URL matches the release origin used by `scripts/postinstall.js`.

## Guardrails

- Prefer additive changes; do not break the release asset naming contract unintentionally.
- If you change release artifacts or CLI binary name, update both workflow and postinstall script in the same PR.
- Keep agent-facing failures specific and actionable, especially for `procoder-return`.
- Favor integration tests for real Git behavior over mocked unit-only coverage for exchange flows.
