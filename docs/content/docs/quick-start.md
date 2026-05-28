---
title: Quick Start
linkTitle: Quick Start
weight: 1
---

Five minutes from install to your first release.

## 1. Install

```sh
brew tap stratt-sh/tap
brew install stratt
stratt version
```

If you don't have Homebrew, use the install script (POSIX shells, macOS + Linux):

```sh
curl -fsSL https://stratt.sh/install.sh | sh
```

Or grab a binary directly from the [releases page](https://github.com/zebpalmer/stratt/releases). For CI/CD usage see [Using stratt in CI](/docs/ci).

## 2. See what stratt sees

`cd` into any repo and run:

```sh
stratt doctor
```

You'll get a detected-stacks list plus a table of every universal command and the backend stratt resolved for it. For a Python+UV repo with Kustomize overlays and MkDocs, that table looks like:

```text
Resolved commands:
  build    → uv build
  test     → uv run pytest
  lint     → uv run ruff check
  format   → uv run ruff format
  style    → format + lint
  setup    → uv sync --all-extras
  sync     → uv sync
  lock     → uv lock
  upgrade  → uv lock --upgrade
  clean    → remove build/cache artifacts per detected stacks
  release  → native bump engine (reads [tool.bumpversion])
  deploy   → kustomize image bump (deploy/overlays/<env>/kustomization.yaml)
  docs     → mkdocs build
  all      → format + lint + test + docs
```

`doctor` is your trust anchor: what it shows is exactly what every subcommand will do in this repo.

## 3. Run universal commands

These work in any detected stack with no config:

```sh
stratt build       # build the project using the detected toolchain
stratt test        # run tests
stratt lint        # run linters
stratt format      # run formatters
stratt style       # format + lint together
stratt all         # full verification (format, lint, test, docs)
```

If a command's backend isn't on your `$PATH` (e.g., `uv` not installed), `stratt doctor` flags it as `[tool not on PATH]` — and the command itself fails with the underlying tool's error.

## 4. Add custom tasks

Create a `stratt.toml` at your repo root:

```toml
[tasks.deploy-staging]
description = "Roll the staging environment"
run = "kubectl apply -k deploy/overlays/staging"

[helpers.preflight]
tasks = ["test", "lint"]

[tasks.deploy-prod]
description = "Roll prod after preflight"
tasks = ["preflight"]
run = "kubectl apply -k deploy/overlays/prod"
```

Then:

```sh
stratt help                    # shows your new tasks
stratt run deploy-staging
```

**Tasks live in one namespace.** Built-ins and your custom tasks share it. `deploy-prod` can list `preflight` as a dep just like it could list `test`. Helpers (in `[helpers.X]`) are hidden from `stratt help` but still callable.

Override a built-in by reusing its name with a `run` field, or augment it by setting `before`/`after`/`tasks` without `run`:

```toml
# Override
[tasks.test]
run = "pytest -m 'not slow'"

# Augment — built-in body still runs, with hooks around it
[tasks.test]
before = ["docker compose up -d testdb"]
after  = ["docker compose down"]
```

Disable a built-in entirely with `enabled = false`.

## 5. Release

For repos with `[tool.bumpversion]` or `[bump]` in `stratt.toml`:

```sh
stratt release            # interactive — prompts for patch/minor/major
stratt release patch      # non-interactive
stratt release minor --ci # CI mode: no prompts at all
```

What happens:

1. **Branch check** — must be on `main` (or `master` if no `main`; configurable via `[release] branch = "..."`).
2. **Clean tree check** — no uncommitted changes.
3. **`stratt all`** runs as a pre-release gate — tests, lint, format check, docs build (whatever's detected).
4. **Re-check clean tree** — if a formatter touched files during step 3, abort so you can commit those first.
5. **Bump** version in your configured files.
6. **Commit + tag + push** the release. GitHub Actions takes over from there.

The post-push output makes the result obvious:

```text
→ pushing commit to origin/main
✓ pushed commit to origin/main
→ pushing tag v1.4.0
✓ pushed tag v1.4.0

✓ Released 1.4.0 — remote is now at v1.4.0.
```

Use `--no-push` to do everything locally and inspect before pushing manually.

## 6. Deploy

Bump a Kustomize image tag without touching `kustomize` CLI or `sed`:

```sh
stratt deploy prod 1.14.1
```

That edits `deploy/overlays/prod/kustomization.yaml` in place (preserving comments and formatting), prints the change, and stops. Add `--commit` to stage and commit in one step:

```sh
stratt deploy prod 1.14.1 --commit --yes
```

For multi-image overlays, pass `--image=<name>` to disambiguate.

## 7. Self-update

```sh
stratt self check     # is a newer version available?
stratt self update    # download, verify, install
```

On Homebrew installs, `stratt self update` checks for an update and offers to run `brew upgrade stratt` for you. Pass `--yes` to skip the prompt; `--ci` to print the command without prompting.

Updates are verified against Sigstore artifact attestations. The trust anchor is compiled into stratt as `zebpalmer/stratt` + the release workflow path — even a compromised GitHub Actions secret can't ship an unsigned binary that stratt accepts.

---

## What next?

- `stratt help` shows all available commands for the current repo.
- `stratt help <command>` shows per-command help.
- `stratt doctor` answers "what would stratt do here?" before you commit.

For the full configuration reference, browse [`stratt.toml` on the repo](https://github.com/zebpalmer/stratt/blob/main/stratt.toml) — stratt dogfoods its own config there.
