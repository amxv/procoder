# CONTRIBUTORS.md

Maintainer notes for this template repository.

## Prerequisites

- Go `1.26+`
- Node `18+`
- npm account with publish rights for the package name in `package.json`
- GitHub repo admin access

## Local development

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

Quick local smoke path:

```bash
make check
make build
make build-helper
./dist/procoder --help
```

Install command locally:

```bash
make install-local
procoder --help
```

`make install-local` installs both `procoder` and `procoder-return_linux_amd64` into `~/.local/bin`.

## Release process

1. Ensure `main` is green:

```bash
make check
```

2. Prepare release tag:

```bash
make release-tag VERSION=0.1.0
```

3. GitHub Actions `release` workflow runs automatically:
- quality checks
- cross-platform `procoder` binary build
- packaged `procoder-return_linux_amd64` helper build
- GitHub release publish
- npm publish

## Required GitHub secret

- `NPM_TOKEN`: npm automation token with publish rights for your package.

Set via GitHub CLI:

```bash
gh secret set NPM_TOKEN --repo amxv/procoder
```

## npm token setup

Create token at npm:

- Profile -> Access Tokens -> Create New Token
- Use an automation/granular token scoped to required package/org

Validate auth locally:

```bash
npm whoami
```

## Notes on package naming

Before first publish, set a package name you control in `package.json`.

- Example unscoped: `"name": "your-cli-name"`
- Example scoped: `"name": "@your-scope/your-cli-name"`
