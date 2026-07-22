---
title: stratt
toc: false
---

# stratt

> One set of commands for every repo, whatever language it's in.

Every repo needs the same handful of commands — *build, test, lint, format, release, deploy* — but each language and toolchain spells them differently. Make targets can paper over that per-repo, but then every Makefile becomes its own dialect. Stratt collapses it all to one vocabulary: detect the toolchain, dispatch to it, and keep the differences between stacks out of your way.

Named for **Eva Stratt**, Project Director of the Petrova Taskforce in Andy Weir's *Project Hail Mary*.

```sh
$ stratt test              # uv run pytest, or go test ./..., or composer test —
                           # whichever the repo actually uses
$ stratt release minor     # bump the version source, commit, tag, push
$ stratt deploy prod 1.14.1   # bump Kustomize image tags
$ stratt doctor            # show exactly what each command will dispatch to
```

## Install

{{< tabs >}}
{{< tab name="Install script" >}}
```sh
curl -fsSL https://stratt.sh/install.sh | sh
```
{{< /tab >}}
{{< tab name="Homebrew" >}}
```sh
brew tap stratt-sh/tap
brew trust stratt-sh/tap   # Homebrew 6+: third-party taps must be trusted
brew install stratt
```
{{< /tab >}}
{{< tab name="Direct download" >}}
Grab a binary from the [releases page](https://github.com/stratt-sh/stratt/releases)
and put `stratt` on your `$PATH`.

If macOS blocks it:

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

`doctor` shows the detected stacks and exactly what each command will run.

{{< cards >}}
{{< card link="/docs/quick-start" title="Quick Start" subtitle="Five minutes from install to first release" >}}
{{< card link="https://github.com/stratt-sh/stratt" title="GitHub" subtitle="Source, releases, issues" >}}
{{< /cards >}}
