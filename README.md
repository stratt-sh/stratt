# stratt

> A polyglot task runner that replaces Makefiles, manages release versions, and handles Kustomize image bumps.

Named for Eva Stratt, Project Director of the Petrova Taskforce in Andy Weir's *Project Hail Mary*.

**Pre-alpha.** Active development; expect breaking changes until v1.0.

Full documentation lives at **[stratt.sh](https://stratt.sh)**.

## Install

```sh
brew install zebpalmer/tap/stratt
```

Or grab a binary from the [releases page](https://github.com/zebpalmer/stratt/releases).

### macOS first-run note

Stratt binaries are not yet notarized. On first run from a direct download, Gatekeeper will quarantine the binary. Clear it with:

```sh
xattr -d com.apple.quarantine /path/to/stratt
```

(Or right-click → Open the first time, then close.) Homebrew installations are unaffected.

## Quickstart

```sh
cd your-project
stratt doctor    # what did stratt detect, and how will it handle each command?
stratt all       # sync, format, lint, test, docs — whatever applies
stratt release   # interactive: patch | minor | major, then commit + tag + push
```

Stratt auto-detects the project's stack and maps universal commands (`build`, `test`, `lint`, `release`, …) to the right backend. Currently recognized: Go, Python+uv, PHP, Docker, Kustomize, MkDocs, Sphinx, Hugo, GitHub Actions, Ansible collections, Ansible roles. Most projects need no configuration; for the rest, a small `stratt.toml` at the repo root overrides whatever you need.

## License

Apache-2.0
