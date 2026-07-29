---
title: Configuration
linkTitle: Configuration
weight: 2
---

Stratt works with no configuration — detection drives everything. When you do need to override or extend its behavior, two files cover it: per-project policy and per-user preference.

## Where project config lives

Project config goes in one of two places:

- **`stratt.toml`** at the repo root — top-level tables.
- **`[tool.stratt]`** in `pyproject.toml` — the same schema, nested under that key.

Having both is an error; stratt refuses to guess which one wins. Python repos should generally use `[tool.stratt]`; everything else uses `stratt.toml`.

Parsing is strict: an unknown key fails at load time rather than being silently ignored. That catches typos and flags config written for a newer stratt than the one you're running.

## Tasks

A task gives a command a name. Built-in tasks (`build`, `test`, `lint`, …) and your own share one namespace, so a custom task can depend on a built-in and vice versa.

```toml
[tasks.deploy-staging]
description = "Roll the staging environment"
run = "kubectl apply -k deploy/overlays/staging"
```

| Field         | Type              | Purpose                                                        |
|---------------|-------------------|----------------------------------------------------------------|
| `description` | string            | Shown in `stratt help`.                                        |
| `run`         | string or list    | The task body — one or more shell commands.                    |
| `tasks`       | list of strings   | Other tasks to run first, in order.                            |
| `before`      | list of strings   | Shell commands run before the body (augment mode only).        |
| `after`       | list of strings   | Shell commands run after the body (augment mode only).         |
| `enabled`     | bool              | Set `false` to disable a task. Default `true`.                 |

Run a task with `stratt run <name>`, or `stratt <name>` for any built-in.

### Override or augment a built-in

Reuse a built-in's name to change it. Adding a `run` field **overrides** the built-in body:

```toml
[tasks.test]
run = "pytest -m 'not slow'"
```

Omitting `run` and setting `before`/`after`/`tasks` **augments** it — the built-in body still runs, wrapped by your hooks:

```toml
[tasks.test]
before = ["docker compose up -d testdb"]
after  = ["docker compose down"]
```

Disable a built-in entirely with `enabled = false`.

## Helpers

Helpers are tasks hidden from `stratt help`. They take the same fields as `[tasks.*]` and are still callable by name and composable as dependencies — useful for shared steps you don't want cluttering the command list.

```toml
[helpers.preflight]
tasks = ["test", "lint"]

[tasks.deploy-prod]
description = "Roll prod after preflight"
tasks = ["preflight"]
run = "kubectl apply -k deploy/overlays/prod"
```

A name can't appear in both `[tasks]` and `[helpers]`.

## Version bump

`stratt release` reads `[bump]` to know what to bump and where. Stratt also accepts a legacy `[tool.bumpversion]` block, so existing bump-my-version repos work unchanged.

```toml
[bump]
current_version = "0.14.1"

[[bump.files]]
filename = "stratt.toml"
search = 'current_version = "{current_version}"'
replace = 'current_version = "{new_version}"'
```

| Field              | Purpose                                                              |
|--------------------|----------------------------------------------------------------------|
| `current_version`  | The version stratt bumps from.                                       |
| `[[bump.files]]`   | Each entry names a `filename` and the `search`/`replace` templates that locate the version in it. A bare `files = [...]` list is also accepted. |
| `tag_prefix`       | Git tag prefix. Default `v`.                                         |
| `message_template` | Commit message for the bump. A default is applied if unset.          |

Run `stratt config migrate-bump` to move a legacy `[tool.bumpversion]` block into stratt's native location.

## Release

The `[release]` table tunes the release flow. Every field is optional; absence means stratt's default.

```toml
[release]
branch = "main"        # release branch; default auto-detects main, then master
remote = "origin"      # push target; default "origin"
push = true            # push commit + tag; default true
commit = false         # create the bump commit; default true — false enables a review-then-merge flow
sync_lockfiles = true  # re-sync ecosystem lockfiles after the bump; default true
```

### Lockfile sync

Ecosystem lockfiles record the project's own version, so a version bump alone leaves them one release behind — the next `uv sync` produces a dangling diff that pollutes whatever commit comes next. With `sync_lockfiles` on (the default), `stratt release` regenerates them after the bump and stages the result inside the release commit:

- **python+uv** — the bump touched `pyproject.toml` and `uv.lock` exists: `uv lock` runs (lockfile only; no virtualenv is touched). uv also owns the version formatting, so prereleases work even though `pyproject.toml` holds `1.2.3-rc.2` while the lock records the PEP 440-normalized `1.2.3rc2`.
- **node+npm** — the bump touched `package.json` and `package-lock.json` exists: `npm install --package-lock-only --ignore-scripts` runs.

The repo root and every declared `[[subprojects]]` path are considered. If a lock command fails, the release aborts before anything is committed or tagged. With `--no-commit`, the synced lockfiles are left uncommitted alongside the bumped files.

This makes the old workaround of listing the lockfile as a `[[bump.files]]` entry unnecessary (and for prereleases it never worked — the search template can't match uv's normalized version string). Likewise, legacy `[tool.bumpversion]` `pre_commit_hooks` like `uv sync` are **not executed** by stratt; the release flow warns when it sees them so the stale config can be deleted.

## Deploy

The `[deploy]` table tunes `stratt deploy`.

```toml
[deploy]
primary_image = "myapp"  # which image to bump when the overlay has several and --image is omitted
push = true              # default true
commit = true            # default true
```

## Requiring a minimum stratt version

```toml
required_stratt = ">= 1.2"
```

Older binaries refuse to run in the repo until upgraded. `version` and `doctor` stay exempt so you can always diagnose a pin. Write it for the current binary with `stratt config require-version`.

## User config

Per-user preferences live in `~/.stratt/config.toml` (override the path with `$STRATT_CONFIG`). This file is for personal defaults, not project policy — keep project rules in `stratt.toml`.

```toml
[update]
channel = "stable"        # "stable" | "prerelease"
auto_check = true         # poll for newer releases; false to disable

[display]
color = "auto"            # "auto" | "always" | "never"
verbosity = "normal"      # "quiet" | "normal" | "verbose" | "debug"

[paths.tools]
uv = "/opt/homebrew/bin/uv"   # pin a specific tool instead of the $PATH choice

[workspace]
root = "~/code"               # see the Workspace page
layout = "{host}/{org}/{repo}"

[release]
push = false              # personal default when the project hasn't pinned push/commit

[deploy]
push = false
```

`[release]` and `[deploy]` accept the same `push`/`commit` overrides as project config so you can opt out of auto-push everywhere without editing each repo. When both set a value, **project config wins** — project policy is sticky.

## Inspecting config

```sh
stratt config show       # print the resolved project configuration
stratt config migrate    # apply auto-fixable deprecations
stratt doctor            # what each command resolves to in this repo
```

Stratt dogfoods its own config — see [`stratt.toml` on the repo](https://github.com/stratt-sh/stratt/blob/main/stratt.toml) for a worked example.
