# Packaging a Rust Project for Fully Offline Development in a Debian 13 x86_64 Sandbox

## Executive summary

A fully offline Rust development bundle has three hard requirements: pinned dependency resolution, a local source of all crate sources (including git dependencies), and a Linux-native Rust toolchain that runs on the target system (Debian 13 x86_64) without fetching anything over the network. Cargo can be forced offline via `--offline/--frozen` or configuration (`net.offline` / `CARGO_NET_OFFLINE`) so you can verify the bundle is truly air-gapped. citeturn10view0turn20view0

The strongest “default” packaging pattern for your constraints is:

1) **Vendor all Cargo sources into the repository** using `cargo vendor` + a checked-in `.cargo/config.toml` source replacement so Cargo never contacts crates.io or git remotes. `cargo vendor` explicitly vendors **crates.io and git dependencies** into a local directory source. citeturn7search18turn2view0turn0search1  
2) **Bundle a Linux x86_64 Rust toolchain** as a set of official Rust distribution artifacts (preferably `.tar.xz`), and install them *locally* inside the unzipped directory using the included `install.sh` scripts (no `rustup` network access needed). Official standalone installers are explicitly “suitable for offline installation,” but include standard documentation and do not offer rustup-style “extra target” management. citeturn11view2  
3) **Treat native system dependencies as a separate layer**: some can be bundled (e.g., `protoc`), many are best handled via Debian packages preinstalled in the sandbox, and a subset can be made self-contained via crate “vendored” features (for example, the `openssl` crate supports a `vendored` feature that builds OpenSSL from source—but that itself requires build tools like a C compiler, `perl`, and `make`). citeturn31view0turn30search2turn30search1

For zip size reduction without sacrificing correctness, the highest-leverage move is to **avoid shipping “global caches”** (like an entire `$CARGO_HOME`) and to **avoid shipping toolchain docs** unless actually needed. Cargo’s own docs note that caching the entire Cargo home is often inefficient because it can duplicate sources both as `.crate` archives and as extracted sources. citeturn18view2

## Recommended default architecture for your exact situation

### Design goals and key assumptions

You want: macOS as the source machine, a locked Debian 13 x86_64 sandbox with **no network**, and zip-only transport. You also want a workflow the remote agent can use to build/test/lint/fix and produce Git-native changes.

This architecture aims to minimize “unknowns” inside the sandbox by making **Cargo resolution** and **Rust toolchain availability** deterministic and offline-first, while explicitly surfacing **native/tooling prerequisites** as an inspectable contract.

### Bundle layout

A practical, CLI-friendly layout:

```text
offline-bundle/
  project/                      # repo working tree (optionally with .git/)
    Cargo.toml
    Cargo.lock
    .cargo/
      config.toml               # generated/managed; forces vendored sources + offline
    vendor/                     # cargo vendor output (crates.io + git sources)
    ... your sources ...
  toolchain-dist/               # compressed official Rust artifacts (do NOT expand on macOS)
    rustc-<ver>-x86_64-unknown-linux-gnu.tar.xz
    cargo-<ver>-x86_64-unknown-linux-gnu.tar.xz
    rust-std-<ver>-x86_64-unknown-linux-gnu.tar.xz
    clippy-<ver>-x86_64-unknown-linux-gnu.tar.xz        # usually clippy-preview internally
    rustfmt-<ver>-x86_64-unknown-linux-gnu.tar.xz        # usually rustfmt-preview internally
    (optional) rust-src-<ver>.tar.xz
  toolchain/                    # installed toolchain prefix after bootstrap (created in sandbox)
  sysdeps/                      # optional bundled native tools (e.g., protoc)
  scripts/
    bootstrap-toolchain.sh
    env.sh
    verify-offline.sh
  README_OFFLINE.md
  bundle-manifest.json
```

Why this structure works:

* `cargo vendor` + source replacement flips Cargo into a “directory source” mode, so dependency sources are local and reproducible with a lockfile. citeturn2view0turn0search1  
* Installing a toolchain into `./toolchain/` avoids any reliance on `~/.cargo`, `~/.rustup`, or privileged paths like `/usr/local`. The Rust installer’s `install.sh` supports `--prefix`, `--destdir`, selecting components, and disabling `ldconfig` (useful without root). citeturn25view0turn26view0  
* Keeping toolchain artifacts compressed and only installing them inside the sandbox helps zip size and avoids macOS/Linux filesystem edge cases during expansion.

### Bootstrap script behavior on Debian

Your `scripts/bootstrap-toolchain.sh` should:

1) Verify required unpack tools exist (`tar`, and ideally `xz`). Prefer `.tar.xz` for size, but fall back to `.tar.gz` if you decide the sandbox may lack `xz`. (Official artifacts are published in multiple formats, and manifests can contain both `.tar.gz` and `.tar.xz` URLs/hashes.) citeturn15view0  
2) Install into a local prefix (e.g., `toolchain/`) using `install.sh --prefix=… --disable-ldconfig`. The installer has an `ldconfig` step and explicitly warns it may fail when not installing as root; disabling it avoids noisy failures. citeturn26view0  
3) Optionally run `rustc --version` and `cargo --version` as a sanity check.
4) Emit a stable environment wrapper `scripts/env.sh`.

Your `scripts/env.sh` should set:

* `PATH` so your bundled `cargo`/`rustc` are used.
* `CARGO_NET_OFFLINE=true` (or set `[net] offline = true`), so Cargo refuses network access even if misconfigured. Cargo supports `net.offline` and the `CARGO_NET_OFFLINE` environment variable. citeturn20view0

You then run project commands as:

```bash
source scripts/env.sh
cd project
cargo build --frozen
cargo test  --frozen
```

`--frozen` is defined as equivalent to `--locked` + `--offline` for Cargo commands like `cargo fetch`, and it is the strongest “no network + no lockfile mutation” mode for verifying correctness. citeturn10view0

### Why this is portable to Debian 13 x86_64

You cannot execute macOS Rust binaries on Linux (different OS ABI and binary formats), so you must ship a **Linux-host** toolchain (`x86_64-unknown-linux-gnu`) for the sandbox. Rust toolchains targeting Linux have published minimum runtime requirements: for Rust 1.64+ the baseline is **glibc ≥ 2.17** and **kernel ≥ 3.2**, applying to the toolchain itself (Cargo/rustc) and to binaries using `libstd`. Debian 13’s glibc and kernel are far newer than these baselines in typical deployments, so official Rust Linux binaries are generally compatible. citeturn32search2

## Cargo vendoring and offline correctness

### Recommended default: `cargo vendor` + source replacement

The Cargo Book’s recommended vendoring mechanism is:

* run `cargo vendor`, which “vendors all crates.io and git dependencies” into a given directory, and
* configure Cargo to replace the default sources with that vendor directory using source replacement. citeturn7search18turn0search1turn2view0

A canonical flow (performed on your macOS machine while you still have internet) looks like:

```bash
cd project

# Ensure you have a lockfile (critical for deterministic offline work).
# If missing, cargo commands like cargo fetch will generate it.
cargo fetch --locked

# Vendor everything into ./vendor and write the config snippet
mkdir -p .cargo
cargo vendor vendor > .cargo/config.toml
```

The Cargo vendor docs show this exact pattern where `cargo vendor` output is redirected into `.cargo/config.toml`. citeturn2view0

Under the hood, the `.cargo/config.toml` will typically use Cargo “source replacement” to replace `crates-io` with a local directory source. Cargo’s source replacement system is the official mechanism for “vendoring” dependency sources. citeturn0search1

### Lockfiles, platforms, and “all-features” concerns

A frequent worry is: “If I generate `Cargo.lock` on macOS, will it include Linux-only dependencies?” Cargo’s dependency resolver explicitly addresses this in the context of generating `Cargo.lock`:

* For lockfile generation, the resolver models **all features of all workspace members as enabled**, so optional dependencies are resolved and present when features are later toggled. citeturn8view3  
* Platform-specific dependencies (those under `[target]` / `cfg(...)`) are resolved “as-if all platforms are enabled,” ignoring the platform expression during resolution for the lockfile. citeturn9view3

This is a major reason vendoring + lockfiles is viable across host OSes: the lockfile is intended to be a comprehensive resolution artifact.

That said, your offline bundle can still fail if the remote agent changes `Cargo.toml` dependencies or enables features that were never represented in the lockfile (for example, in repos where the lockfile is missing or intentionally not maintained). For an offline product workflow, the pragmatic rule is: **include `Cargo.lock` in the bundle and treat it as required**.

### Driving Cargo fully offline inside the sandbox

You want offline correctness to be self-enforcing, not “best effort.”

Use at least two layers:

1) `.cargo/config.toml` ensures sources are vendored and can also enforce `[net] offline = true`. Cargo supports `net.offline` and maps it to the `CARGO_NET_OFFLINE` environment variable. citeturn20view0  
2) Wrap developer commands with `--frozen` (or at least `--locked --offline`) in your scripts. Cargo documents `--offline` and `--frozen` and warns that `--offline` restricts Cargo to locally available crates. citeturn10view0

A “verification” script you ship (`scripts/verify-offline.sh`) should do something like:

```bash
set -euo pipefail
source scripts/env.sh
cd project

# Enforce determinism + no network + no lock changes:
cargo build --frozen
cargo test  --frozen
```

### When `cargo vendor` is sufficient and when it is not

`cargo vendor` is generally sufficient for **source acquisition** of crates.io and git dependencies because it vendors those sources into a directory Cargo can use via source replacement. citeturn7search18turn0search1

It is not sufficient by itself when:

* **Build-time scripts require external tools or system libraries.** Cargo build scripts are compiled and executed during builds, and they commonly emit linker directives (`cargo:rustc-link-search`, etc.). If the build script expects `pkg-config`, CMake, a C toolchain, `protoc`, `libclang`, etc., vendoring Rust sources won’t help. citeturn1search17turn19view0turn31view0turn30search2turn30search1  
* **Native library discovery is required.** For example, the `openssl` crate’s “Automatic” build mode uses `pkg-config` on Unix-like systems to find system OpenSSL; if OpenSSL headers/libs are absent, the build will fail unless you switch to the `vendored` feature or explicitly provide paths. citeturn31view0turn30search27  
* **Your workflow depends on additional Rust components** (clippy, rustfmt) not present in minimal toolchains. The Rust distribution ecosystem often treats these as components/extensions (historically `*-preview` internally) and they must be included deliberately if you want offline lint/format. Rust’s release channel manifests and profiles (minimal/default/complete) exist specifically to describe which components are installed by default and which are optional. citeturn15view0

## Toolchain packaging from macOS to Debian 13 x86_64

### Why a macOS-built toolchain won’t work on Debian

The Rust toolchain is host-platform-specific: a toolchain built for `x86_64-apple-darwin` is not executable on Linux, and vice versa. Rustup’s concept of toolchains encodes host triples (e.g., `stable-x86_64-unknown-linux-gnu`) to distinguish host platforms. citeturn12search3

Therefore, your bundle must include **Linux host** toolchain binaries for `x86_64-unknown-linux-gnu`.

### Recommended default: component-based Linux toolchain bundle + local install

You have two “official” distribution patterns:

* **Standalone installers** (single-release bundles): explicitly “suitable for offline installation,” contain `rustc`, `cargo`, `rustdoc`, standard library, and standard documentation, but don’t provide rustup-style extra targets. citeturn11view2  
* **Rustup distribution components** described by channel manifests: manifests list packages (`rustc`, `cargo`, `rust-std`, etc.), with per-target `.tar.xz` URLs/hashes and a profile system describing minimal/default/complete sets. citeturn15view0turn8view3

For zip size reduction **and** predictable inclusion of clippy/rustfmt, prefer the second pattern in your CLI design:

1) Decide a Rust version (pin it; do not “latest stable” if you want deterministic offline).  
2) Download only the needed Linux x86_64 components from the official distribution site:
   * minimal core: `rustc`, `cargo`, `rust-std`  
   * dev tools: `clippy` and `rustfmt` (often internally `clippy-preview` / `rustfmt-preview` in manifests) citeturn15view0turn27search6  
3) Install them locally in the sandbox using their `install.sh` scripts.

The installer script supports the knobs you need for sandbox-friendly installs:

* `--destdir` (installation root) and `--prefix` (installation prefix) citeturn25view0  
* `--components` / `--without` for selecting which components to install citeturn25view0  
* `--disable-ldconfig` (because it tries to run `ldconfig` on Linux and warns it may fail when not root) citeturn26view0

This gives you a toolchain located entirely under your unzipped directory, with no network, no root, and no dependency on a pre-existing Rust install.

### “Portable into Debian 13” checklist

When bootstrapping inside the sandbox, include checks that fail fast with actionable errors:

* `uname -m` is `x86_64`
* `ldd --version` (optional) is glibc ≥ 2.17 (Rust’s documented baseline for Linux toolchains and libstd-linked binaries) citeturn32search2  
* required basic shell tools exist (`mkdir`, `grep`, `sed`, etc.). The installer explicitly checks for common tools like `mkdir`, `printf`, `cut`, `grep`, `uname`, `tr`, `sed`, `chmod`, `env`, and `pwd`. citeturn25view0  
* `rustc --version` and `cargo --version` run successfully

### rustup-managed toolchains as a “good alternative” (but not the default)

Rustup is excellent for selecting toolchains and components, and it supports relocating its state via environment variables:

* `CARGO_HOME` and `RUSTUP_HOME` can be set before running `rustup-init` to control where toolchains and Cargo caches live. citeturn18view0  
* `RUSTUP_DIST_SERVER` can be pointed at a local mirror instead of `https://static.rust-lang.org`. citeturn16view0

However, in your “zip-only, no network” sandbox, rustup is only viable if you **pre-populate** the toolchains and components (for example, by running rustup in a Linux environment you control, then copying the resulting `RUSTUP_HOME`/toolchain directories into the zip). That can be a very good approach if your product can depend on a Linux build/export step (VM/Docker on macOS), but it’s less attractive if you want the entire bundle assembly to happen natively on macOS without running Linux.

## Native system dependencies and what can realistically be zipped

Offline Rust development gets hard when crates depend on native tooling. Vendoring Rust crates solves “where do I get the Rust source,” but it does not solve “where do I get the system compiler, headers, and external tools that build scripts expect.” Cargo build scripts execute during builds and can emit native linking directives; they often act as the bridge to non-Rust dependencies. citeturn1search17turn19view0

### Practical categorization for a CLI product

#### Dependencies you usually should not try to zip

Large, distro-integrated toolchains and libraries are generally poor candidates to bundle in a zip for a locked-down Debian sandbox:

* Full C/C++ toolchains, system linkers, and libc toolchains (size, complexity, and ABI expectations)
* Full LLVM/Clang installs (often hundreds of MB once runtime libs are included)

If the project needs these, the most robust approach is to make them **sandbox prerequisites** (documented and checked by your scripts) rather than trying to smuggle them in via zip—unless you are willing to ship something Nix-like (see below).

#### Dependencies you often can zip successfully

Some “single-binary” or “small bundle” tools can be shipped and pointed to via environment variables:

* **`protoc`**: `prost-build` searches `PATH` for `protoc` and supports `PROTOC=/path/to/protoc` for nonstandard installs. This makes `protoc` a good candidate for bundling into `sysdeps/bin/` and exporting `PROTOC`. citeturn30search2  
* Other single-binary build utilities (project-dependent)

#### Dependencies that can be made self-contained via Rust crate features

The `openssl` crate is a representative example of how Rust crates often offer a “vendored” escape hatch:

* With `features = ["vendored"]`, it uses `openssl-src` to compile and statically link OpenSSL. citeturn31view0  
* But that vendored build **requires a C compiler, `perl` (and perl-core), and `make`**. citeturn31view0  
* Without vendoring, `openssl-sys` can use `pkg-config` to find system OpenSSL on Unix-like systems. citeturn31view0turn30search27  
* It also documents manual override environment variables (`OPENSSL_DIR`, `OPENSSL_LIB_DIR`, etc.) and cross-compilation prefixed variants. citeturn31view0

For a locked Debian sandbox, a realistic strategy is:

* If the sandbox likely includes `pkg-config` + `libssl-dev`, do not vendor OpenSSL; just document prerequisites.
* If you cannot rely on system OpenSSL, consider switching to `vendored`, but then you must ensure the sandbox has the required build tools.

#### `bindgen` / libclang as a special pain point

If your dependency graph includes `bindgen`, it “leverages libclang” and requires Clang 9.0+ to preprocess/parse headers. citeturn30search1  
This is often the dependency that breaks “zip everything” dreams because libclang is not tiny.

Best-practice options include:

* Prefer pre-generated bindings checked into the repo (skip bindgen at build time), when feasible.
* Require `libclang` as a sandbox prerequisite and fail-fast with a clear message if missing.
* If you must bundle it, understand the size hit and dynamic library path issues (and test on Debian 13 specifically).

## Comparison of approaches with pros, cons, and size implications

| Approach | What you ship in the zip | Pros | Cons / risks | Size impact |
|---|---|---|---|---|
| **Recommended default: `cargo vendor` + component-based Linux toolchain install into local prefix** | `project/` + `vendor/` + `.cargo/config.toml` + selected Rust `.tar.xz` components + bootstrap scripts | No network needed; deterministic builds (`--frozen`); smallest *correct* toolchain bundle because you can exclude docs; no rustup dependency in sandbox | Requires bootstrap tooling (`tar`, `xz`); still depends on native toolchain/libs for `build.rs`-driven deps | Best overall: avoids docs + avoids `$CARGO_HOME` bloat citeturn18view2turn15view0turn25view0 |
| **Good alternative: pre-populated rustup toolchain directory created on Linux (VM/Docker), then zipped** | `project/` + `vendor/` + a ready-to-run `RUSTUP_HOME`/toolchain dir | Very ergonomic; rustup profiles (minimal/default) can manage components; easy to include clippy/rustfmt | Requires a Linux build/export step; you must ensure rustup never tries to auto-install/update in sandbox | Potentially small if you use “minimal profile + chosen components”; channel profiles exist for this purpose citeturn15view0turn18view0 |
| **Local registry / mirror instead of vendoring** | local registry index + `.crate` archives | Closer to how Cargo normally works; can support sparse index/mirroring tools | More moving parts than vendoring; index management can inflate size; more error-prone offline | Often larger than vendoring because you need an index; Cargo supports both local registries and directory vendoring citeturn0search1turn18view2 |
| **High-risk / not recommended: ship entire `$CARGO_HOME` cache** | `~/.cargo/registry`, `~/.cargo/git`, etc. | Easy to implement (copy a directory) | Cache formats are not stable; likely huge; Cargo stores sources twice (archives + extracted) and includes unrelated crates | Typically worst for size; Cargo docs explicitly warn whole `$CARGO_HOME` caching is often inefficient citeturn18view2 |
| **High-risk in locked sandboxes: Nix / crate2nix-based hermetic environment** | Nix closure + Rust build via Nix | Potentially most hermetic/reproducible; crate2nix can use Cargo.lock and cache per crate citeturn29search2turn29search6 | Often infeasible without `/nix` + daemon/root setup; multi-user mode involves a daemon and cache trust limits citeturn29search7turn29search23turn29search3; “rootless Nix” relies on user namespaces/PRoot and may be blocked citeturn29search15 | Can be very large; heavy operational footprint |
| **Not applicable here: `cross` / `cargo-chef`** | Docker/Podman-centric workflows | Great for CI/container builds | Your sandbox cannot run a container engine; `cross` defaults to Docker/Podman citeturn29search0; `cargo-chef` is primarily for Docker layer caching citeturn29search1 | Not relevant for zip-only sandbox |

## Final recommendations for your CLI tool, including size-reduction strategy

### Recommended default workflow your CLI should implement

Your CLI should implement a **two-phase** model: *pack on macOS*, *bootstrap + verify on Debian*.

#### Pack phase on macOS (connected machine)

1) **Validate / lock resolution**
   * Ensure `Cargo.lock` exists and is up to date (fail if missing unless `--generate-lockfile` is explicitly allowed).
   * Optionally run `cargo fetch --locked` to materialize all registry + git deps locally before vendoring; Cargo states this downloads dependencies and enables subsequent offline operation unless the lock file changes. citeturn10view0

2) **Vendor Rust dependencies**
   * Run `cargo vendor` and write `.cargo/config.toml` (your CLI should own/overwrite this file, or write into a dedicated include file it manages).
   * This is best practice for vendoring crates.io + git deps. citeturn7search18turn2view0

3) **Download Linux toolchain artifacts**
   * Prefer `.tar.xz` artifacts listed by the official channel manifests (smaller than `.tar.gz` and explicitly described in manifest layout). citeturn15view0
   * Download only what you need:
     * Core: `rustc`, `cargo`, `rust-std`
     * Dev tools: clippy + rustfmt (note rustup manifests often rename these from `rustfmt` to `rustfmt-preview`, and similarly for clippy) citeturn15view0turn27search6
   * Record checksums/hashes in `bundle-manifest.json` (the channel layout describes that manifests include hashes and sometimes both `.tar.gz` and `.tar.xz` URLs). citeturn15view0

4) **Optionally download “zip-friendly” native tools**
   * Example: bundle `protoc` if your dependency tree uses `prost-build`, and set `PROTOC` in `scripts/env.sh` accordingly. citeturn30search2

5) **Generate sandbox scripts**
   * `bootstrap-toolchain.sh`: extracts and installs toolchain components into `./toolchain/` using installer script flags like `--prefix`, `--destdir`, `--disable-ldconfig`. citeturn25view0turn26view0  
   * `env.sh`: sets `PATH`, `CARGO_NET_OFFLINE=true`, and any required native env vars (`PROTOC`, `OPENSSL_DIR`, etc.) citeturn20view0turn31view0turn30search2  
   * `verify-offline.sh`: runs `cargo build/test/clippy/fmt` in `--frozen` mode.

#### Bootstrap + verify phase on Debian (air-gapped sandbox)

Run:

```bash
./scripts/bootstrap-toolchain.sh
source ./scripts/env.sh
cd project
./scripts/verify-offline.sh
```

This yields a local toolchain, local vendored sources, and enforced offline Cargo networking rules.

### Size reduction: concrete, high-leverage tactics

Your bundle size will mostly be dominated by `vendor/` and `toolchain-dist/`. The best-practice reductions that preserve correctness:

* **Do not ship `$CARGO_HOME`** as your primary offline mechanism. Cargo’s docs explain the Cargo home is a cache (registry index, `.crate` files, extracted sources, git db/checkouts), and that caching the entire directory is often inefficient because it duplicates sources in both archive and extracted form. Vendoring avoids shipping that duplication. citeturn18view2  
* **Prefer component-based toolchain downloads over standalone installers** when size matters. Standalone installers include standard documentation by design. citeturn11view2  
* **Use `.tar.xz` artifacts when available** (the channel manifest layout includes `xz_url`/`xz_hash` fields as a first-class option). citeturn15view0  
* **Install into the bundle, not system paths**, and disable `ldconfig` to avoid failures and extra surface area: the installer’s `ldconfig` step may fail without root and is controllable. citeturn26view0  
* **Make lint/format components configurable**: clippy/rustfmt are valuable but nontrivial in size. Provide CLI options like `--with clippy,rustfmt` or `--without clippy,rustfmt`. Rust’s distribution model explicitly distinguishes minimal vs default vs complete profiles and optional components. citeturn15view0  
* **Optional: feature-gate native dependencies**. For example, if OpenSSL is only needed under a feature flag, consider building the smallest offline bundle around the common feature set—while preserving a “max correctness” mode for CI-like full-feature verification. Cargo’s resolver models all features for lockfile generation to make later toggling possible, but shipping fewer crates can still reduce vendor size if you intentionally constrain the dependency graph. citeturn8view3

### What your CLI should output as product-quality artifacts

To make this reliably productizable, your CLI should emit:

* `bundle-manifest.json` with:
  * Rust toolchain version, target triple (`x86_64-unknown-linux-gnu`), list of components, and hashes (from manifests) citeturn15view0turn32search2  
  * Vendoring mode used (directory source replacement) citeturn0search1turn2view0  
  * An explicit “native prerequisites” section with checks (e.g., `pkg-config`, `clang/libclang`, `make`, `perl`, `protoc`) tied to detected crates if possible

* `README_OFFLINE.md` that tells the agent:
  * “Run `bootstrap-toolchain.sh`, source `env.sh`, then run verify script.”
  * “Never run plain `cargo build`—use `--frozen` or the wrapper” (enforced anyway)
  * Native dependency notes, pointing to canonical env vars when supported (e.g., `PROTOC`, `OPENSSL_DIR`) citeturn30search2turn31view0

### Alternatives worth supporting as optional modes

If you want your CLI to serve more advanced users without burdening the default path, consider these opt-in modes:

* **“Linux-export” mode using rustup inside a Linux VM/container**: can generate a fully installed toolchain directory using rustup profiles (minimal + selected components) and then copy it into the zip. Rustup supports relocating `CARGO_HOME`/`RUSTUP_HOME`, which helps create self-contained bundles. citeturn18view0  
* **“Mirror-based” enterprise mode**: tools like **romt (Rust Offline Mirror Tool)** are designed to mirror Rust toolchains and crates.io artifacts for offline contexts, and could feed your pack step. citeturn21search29  
* **Nix/crate2nix experimental mode**: only if you can guarantee the sandbox supports Nix installation constraints (often false in locked containers). crate2nix can generate Nix build files for Cargo projects, but Nix multi-user installations rely on a daemon and have security/trust constraints around binary caches, and rootless approaches require kernel support (user namespaces) that many sandboxes disable. citeturn29search2turn29search23turn29search3turn29search15

### High-risk / not recommended defaults

For your “zip-only, no internet” target, avoid making these the default:

* **Shipping the entire Cargo cache** (`$CARGO_HOME`) as your offline mechanism (size blowup + cache format fragility + duplication). citeturn18view2  
* **Depending on container tooling** (`cross`, Docker-based caching like cargo-chef) inside the target sandbox. `cross` defaults to Docker/Podman, and cargo-chef’s core value is Docker layer caching—neither aligns with a locked container without nested container runtime. citeturn29search0turn29search1  
* **Assuming native deps can always be zipped**: `bindgen`/libclang and many system libraries are realistically “environment prerequisites” unless you are willing to ship a full hermetic toolchain stack. citeturn30search1turn31view0