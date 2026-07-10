<!-- BEGIN stratt (managed — run `stratt agents sync`, do not edit) -->
This project uses **stratt** as its task runner. Always prefer stratt
verbs over the underlying tools — `stratt test`, not `go test`/`pytest`;
`stratt build`, not `make`/`cargo build` — stratt detects the toolchain
and dispatches, so behavior matches CI and every other repo here.

Verbs: `build` · `test` · `lint` · `format` · `style` · `all` (full
verification suite) · `run <task>` (custom tasks) · `release patch|minor|major`.
Non-interactive: `--ci` (no prompts, fail loudly) and `-y` (auto-confirm).

Before your first stratt command, run `stratt agents context` — it prints
this repo's resolved command map and conventions. Docs: https://stratt.sh
<!-- END stratt -->
