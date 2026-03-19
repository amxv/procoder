# pro-coder

Minimal template for shipping a Go CLI with:

- a local command runner (`Makefile`)
- npm global install wrapper (`bin/pro-coder.js`)
- automatic GitHub Release + npm publish on tag

## Install (template example)

```bash
npm i -g @amxv/pro-coder
pro-coder --help
```

## Commands in this starter

```bash
pro-coder --help
pro-coder hello
pro-coder hello <name>
pro-coder version
```

## Customize this template

1. Rename your command and entrypoint:
- `cmd/pro-coder`
- `bin/pro-coder.js`
- `package.json` (`bin`, `config.cliBinaryName`)
- `.github/workflows/release.yml` (`CLI_BINARY`)

2. Update module + repo identity:
- `go.mod` module path
- `package.json` (`name`, `repository`, `homepage`, `bugs`)

3. Replace starter logic:
- `internal/app/app.go`
- `internal/app/app_test.go`

4. Keep release flow:
- push tags like `v0.2.0`
- workflow builds binaries + creates GitHub release + publishes npm

## Project layout

- `cmd/pro-coder/main.go`: CLI entrypoint
- `internal/app/`: command logic
- `scripts/postinstall.js`: installs binary from GitHub release (falls back to local `go build`)
- `.github/workflows/release.yml`: automated release pipeline
- `AGENTS.md`: instructions for coding agents
- `CONTRIBUTORS.md`: maintainer/release operations

See `AGENTS.md` and `CONTRIBUTORS.md` for complete dev/release instructions.
