# stratt

> One set of commands for every repo, whatever language it's in.

`build`, `test`, `lint`, `release`, `deploy` — the same commands whether the repo is Go, Python, or PHP. Stratt detects the toolchain and dispatches; you don't think about it. It also manages release versions and bumps Kustomize image tags, replacing the per-repo Makefile.

Named for Eva Stratt, Project Director of the Petrova Taskforce in Andy Weir's *Project Hail Mary*.

**Pre-alpha.** Active development; expect breaking changes until v1.0.

Full documentation lives at **[stratt.sh](https://stratt.sh)**.

## Install

Linux and macOS are supported (Windows via WSL).

```sh
curl -fsSL https://stratt.sh/install.sh | sh
```

The script verifies the release checksum — and, when the `gh` CLI is on PATH, the Sigstore build attestation — before installing to `~/.local/bin` (or `/usr/local/bin` as root; `--dir` overrides).

Or via Homebrew:

```sh
brew tap stratt-sh/tap
brew trust stratt-sh/tap   # Homebrew 6+: third-party taps must be trusted
brew install stratt
```

Without the `brew trust` step, Homebrew 6 refuses to install the cask — and silently skips it during `brew upgrade`.

Migrating from a brew install to the script? Just run the script — it removes the old cask after installing.

Or grab a binary from the [releases page](https://github.com/stratt-sh/stratt/releases).

### macOS first-run note

Stratt binaries are not yet notarized. A binary downloaded with a *browser* gets quarantined by Gatekeeper on first run. Clear it with:

```sh
xattr -d com.apple.quarantine /path/to/stratt
```

(Or right-click → Open the first time, then close.) The install script and Homebrew installs are unaffected — curl doesn't set the quarantine attribute, and the cask strips it on install.

## Quickstart

```sh
cd your-project
stratt doctor    # what did stratt detect, and how will it handle each command?
stratt all       # sync, format, lint, test, docs — whatever applies
stratt release   # interactive: patch | minor | major, then commit + tag + push
```

Stratt auto-detects the project's stack and maps universal commands (`build`, `test`, `lint`, `release`, …) to the right backend. Currently recognized: Go, Python+uv, Node+npm, PHP, Docker, Kustomize, MkDocs, Sphinx, Hugo, GitHub Actions, and Ansible collections, roles, and playbooks. Most projects need no configuration; for the rest, a small `stratt.toml` at the repo root overrides whatever you need.

## License

Apache-2.0
