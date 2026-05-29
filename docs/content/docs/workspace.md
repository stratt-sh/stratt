---
title: Workspace
linkTitle: Workspace
weight: 3
---

Stratt can manage where repositories live on disk. Point it at a workspace root, pick a layout, and `stratt clone` puts every repo at a predictable path. A handful of read-only commands then operate across the whole tree.

## Configure the workspace

The layout lives under `[workspace]` in `~/.stratt/config.toml`:

```toml
[workspace]
root = "~/code"
layout = "{host}/{org}/{repo}"
```

| Key      | Meaning                                                                 |
|----------|-------------------------------------------------------------------------|
| `root`   | Directory that holds every cloned repo.                                 |
| `layout` | Template for each repo's path beneath `root`. Default `{host}/{org}/{repo}`. |

The layout supports three placeholders — `{host}`, `{org}`, `{repo}` — in any arrangement. Common choices:

| Layout                | A repo lands at                          |
|-----------------------|------------------------------------------|
| `{host}/{org}/{repo}` | `~/code/github.com/stratt-sh/stratt`     |
| `{org}/{repo}`        | `~/code/stratt-sh/stratt`                |
| `{repo}`              | `~/code/stratt`                          |

You don't have to write this file by hand. The first workspace command you run with no `[workspace]` configured prompts for the root and layout (when stdin is a terminal) and saves your choices.

## clone

`stratt clone` wraps `git clone` and computes the destination from the URL and your layout:

```sh
stratt clone https://github.com/stratt-sh/stratt
# → ~/code/github.com/stratt-sh/stratt
```

Flags after the URL pass straight through to `git clone`. To bypass layout resolution, give an explicit target as the second argument — stratt forwards it to git untouched:

```sh
stratt clone https://github.com/stratt-sh/stratt ./somewhere-else
```

## workspace list

List every git repo under `root`, with its path (relative to the root) and a one-line description:

```sh
stratt workspace list
```

```
12 repos under /home/you/code

github.com/stratt-sh/stratt  One set of commands for every repo, whatever language it's in.
github.com/acme/widget       Builds widgets.
…
```

The description is read from each repo's README — the blockquote tagline beneath the title, the opening sentence, or failing those the project name. Nothing is shown for a repo with no usable README. The command is strictly read-only and offline: it walks the tree and reads a few lines of each README, touching neither git nor the network.

This is mainly here to make a workspace legible to AI coding agents: an agent working in one repo can run `stratt workspace list` to discover sibling repos and where they live when a task needs code or context from elsewhere in the tree. See [Agents](../agents/).

## workspace status

Scan every git repo under `root` and report which ones have work that isn't safely on a remote — uncommitted changes, or local commits that were never pushed.

```sh
stratt workspace status
```

The command is strictly read-only. It never commits, pushes, fetches, or modifies anything; it only tells you where work is waiting. "Unpushed" is measured against your last-known remote-tracking refs with no network access. Pass `--fetch` to refresh those refs first for an accurate count:

```sh
stratt workspace status --fetch
```

`--fetch` makes network connections but still changes nothing locally. A repo whose fetch fails is reported with a note that its count may be stale, rather than aborting the scan.

## workspace organize

The inverse of `clone`: it reads each repo's `origin` remote, computes the path your layout would have given it, and moves repos that sit elsewhere into place.

```sh
stratt workspace organize          # dry run — prints what would move, changes nothing
stratt workspace organize --apply  # actually move the directories
```

Moving a repo is a plain directory rename — git's internals are path-independent — and parent directories left empty are pruned afterward. A repo is left in place (and reported with a reason) when it has no `origin`, an origin that can't be parsed into host/org/repo, or a target path that's already occupied. A move across filesystems is reported rather than attempted; relocate those by hand.
