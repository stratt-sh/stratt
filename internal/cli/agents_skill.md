---
name: stratt
description: In any repo with a stratt.toml or a "stratt" block in AGENTS.md, use stratt's verbs as the task runner instead of the underlying tool. Check for stratt before running any build/test/lint/format/release/deploy command — before reaching for go test, pytest, uv run, ruff, npm/pnpm/yarn, composer, cargo, goreleaser, or make.
---

# stratt — the universal task runner

One set of commands — `build`, `test`, `lint`, `format`, `release`, `deploy` —
that work the same in every repo, whatever the language.

**Prefer stratt's verbs over the underlying tool:** `stratt test`, not
`pytest`/`go test`; `stratt build`, not `make`/`cargo build`.

**Run `stratt agents context` first** — it prints the verbs, the flags, and the
resolved command map for *this* repo. That's the source of truth; read it
rather than guessing.

Docs: <https://stratt.sh>
