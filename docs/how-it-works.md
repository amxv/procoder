# How procoder Works

`procoder` exists to support an offline Git exchange between:

- your local repository
- ChatGPT's locked-down coding sandbox

The sandbox is useful because it can run code and common developer tooling, but it cannot directly talk to your Git remote or your local filesystem over the network. `procoder` works around that by treating the exchange like a portable Git handoff.

## The Core Idea

The workflow has three phases:

1. `procoder prepare` exports a sanitized Git repo plus a prepared task branch.
2. ChatGPT works in that exported repo and runs `./procoder-return`.
3. `procoder apply` verifies and imports the returned Git bundle into the original repo.

The important design choice is that `procoder` moves Git state, not just file diffs.

That means:

- ChatGPT can inspect real history
- ChatGPT can commit normally
- you receive real commits and refs back locally

## What `prepare` Produces

`procoder prepare` creates a task package named like:

```text
./procoder-task-<exchange-id>.zip
```

Inside that zip is a sanitized export repository with:

- tracked files from your repo
- local branches and tags for read-only context
- a prepared writable task branch:
  `refs/heads/procoder/<exchange-id>/task`
- exchange metadata in `.git/procoder/exchange.json`
- the `procoder-return` helper binary at repo root

It intentionally leaves out things that should not travel into the sandbox, such as:

- remotes
- credentials
- hooks
- reflogs
- ignored and untracked working tree junk

This is why ChatGPT can work offline in a meaningful way: the repo it receives is already self-contained enough to inspect history, edit code, and commit.

## What `procoder-return` Produces

After ChatGPT makes commits, it runs:

```bash
./procoder-return
```

That helper validates the exported repo and creates a return package named like:

```text
./procoder-return-<exchange-id>.zip
```

The return package contains two files:

```text
procoder-return.json
procoder-return.bundle
```

`procoder-return.json` records which refs changed and what old and new commit IDs are expected.

`procoder-return.bundle` is the portable Git payload containing the new objects needed to move those refs forward.

The helper only returns refs inside the exchange branch family:

```text
refs/heads/procoder/<exchange-id>/*
```

It also requires every returned ref to descend from the prepared base commit. That keeps the import predictable and prevents unrelated branch rewrites from being smuggled back in.

## Why The Return Package Is Small

The return package is incremental.

`procoder-return` does not zip the whole repository again. It builds a Git bundle that only contains the new objects created after the exchange started.

That gives `procoder` two important properties:

- return packages are much smaller than full repo exports
- local apply can reason about the exact refs and commits being imported

## What `apply` Does

Back in the original repository, you run:

```bash
procoder apply procoder-return-<exchange-id>.zip
```

`apply` does not blindly trust the zip. It:

1. extracts the return package
2. validates `procoder-return.json`
3. runs `git bundle verify`
4. fetches the bundle into a temporary internal namespace
5. checks that the imported refs match the manifest exactly
6. computes the destination refs
7. updates local refs atomically when safe

By default, it updates the prepared task branch directly. If that branch moved locally, `procoder` fails with a clear error instead of merging or overwriting silently.

That default policy is intentionally simple:

- update when safe
- otherwise fail and tell the user what to do next

## The Safety Model

The exchange is intentionally broad for read-only context and narrow for writable output.

Broad context:

- all local heads
- all local tags

Narrow mutable output:

- only `refs/heads/procoder/<exchange-id>/*`

This means ChatGPT can look around the repo history for context, but it cannot return arbitrary branch or tag mutations as part of the normal V1 workflow.

## Why This Feels Like ChatGPT Committed To Your Repo

In practice, the user experience is:

- you prepare a task branch locally
- ChatGPT commits on that branch in the exported repo
- you apply the incremental result locally

So the final state in your repository is a real branch with real commits created by ChatGPT's work in the sandbox. The network barrier is worked around by packaging the exchange as portable Git data instead of trying to connect the sandbox to your remote.

## Related Docs

- [Command Reference](commands.md)
- [README](../README.md)
