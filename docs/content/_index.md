---
title: stratt
toc: false
---

# stratt

> A polyglot task runner that replaces Makefiles, manages release versions, and handles Kustomize image bumps.

Named for **Eva Stratt**, Project Director of the Petrova Taskforce in Andy Weir's *Project Hail Mary* — given near-unilateral authority to do whatever was necessary. `stratt` takes the same posture toward your repos: one decisive operator that cuts through Makefile sprawl, runs the universal targets, manages release versions, and ships deploys.

{{< callout type="warning" >}}
**Pre-alpha.** Stratt is in active early development. Expect breaking changes until v1.0.
{{< /callout >}}

## The idea

Every repo needs the same handful of commands — *build, test, lint, format, release, deploy* — but each language and toolchain spells them differently. Make targets capture the variance per-repo, but every repo's Makefile becomes its own dialect.

Stratt collapses that to one vocabulary. The differences between toolchains live in stratt's detection layer; they're invisible to the user.

```sh
$ stratt test              # uv run pytest, or go test ./..., or composer test —
                           # whichever the repo actually uses
$ stratt release minor     # bump the version source, commit, tag, push
$ stratt deploy prod 1.14.1   # bump Kustomize image tags
$ stratt doctor            # show exactly what each command will dispatch to
```

Two Go projects in your fleet can have completely different release flows (one bump-my-version-driven, one goreleaser-driven) — same `stratt release` command for both. Stratt detects the right engine and dispatches.

## Highlights

- **One vocabulary, every stack.** Go, Python+UV, PHP, Docker, Kustomize, MkDocs/Sphinx. Multi-stack repos are normal.
- **Zero config when possible.** Detection drives behavior. `stratt.toml` is optional.
- **Single static binary.** No Python or Node runtime required to use stratt itself.
- **Secure self-update.** Sigstore artifact attestations verified on every update. Homebrew users get an automatic dispatch to `brew upgrade`.
- **Composable tasks.** Built-in commands and your own custom tasks share one namespace. Override or augment any built-in.

## Install

{{< tabs >}}
{{< tab name="Homebrew" >}}
```sh
brew tap stratt-sh/tap
brew install stratt
```
{{< /tab >}}
{{< tab name="Direct download" >}}
Grab a binary for your platform from the [releases page](https://github.com/stratt-sh/stratt/releases),
extract it, and put `stratt` somewhere on your `$PATH`.

On macOS first-run you may need to clear Gatekeeper quarantine:

```sh
xattr -d com.apple.quarantine /path/to/stratt
```
{{< /tab >}}
{{< /tabs >}}

## Try it

```sh
cd <any-repo-you-have>
stratt doctor
```

`doctor` reports the detected stacks and the resolved backend for every universal command. That's stratt's contract: what doctor shows is what every command will do.

{{< cards >}}
{{< card link="/docs/quick-start" title="Quick Start" subtitle="Five minutes from install to first release" >}}
{{< card link="https://github.com/stratt-sh/stratt" title="GitHub" subtitle="Source, releases, issues" >}}
{{< /cards >}}
