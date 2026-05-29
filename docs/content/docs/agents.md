---
title: AI agents
linkTitle: AI agents
weight: 4
---

Because stratt gives every repo the same verbs, an AI coding agent only has to learn one interface instead of rediscovering each project's build and test commands. The `stratt agents` namespace makes that interface discoverable.

## agents context

One command prints everything an agent needs to drive stratt here: a short orientation on what stratt is and how to use it, followed by the live command map for the current repo — detected stacks and the backend each verb resolves to.

```sh
stratt agents context
```

Point an agent at this once and it has the resolved commands, the relevant flags (`--ci`, `--yes`, `--no-push`), and the exit-code convention. Like `doctor`, it surfaces config or detection problems inline rather than failing, so the agent still gets whatever is knowable.

If you've configured a [workspace](../workspace/) root, `agents context` ends with a short **workspace** section: it notes that the repo lives in a stratt-managed workspace, gives the layout, and points the agent at `stratt workspace list` to discover sibling repos. The section is omitted entirely when no `[workspace]` is configured — there's nothing to describe and no tree to list.

## agents init and sync

`stratt agents init` adds a short stratt block to the repo's `AGENTS.md`, telling any agent that reads it that stratt is in use and to run `stratt agents context` to learn the commands.

```sh
stratt agents init    # add the stratt block to AGENTS.md
stratt agents sync    # rewrite the block if stratt's boilerplate has changed
```

The block is a **pointer, not a copy** of the command map. It carries no repo-specific detail, so it rarely changes — the live map is always fetched fresh by `agents context`. The block is delimited by HTML-comment markers, which are valid Markdown, omitted from rendered output, and stripped from `CLAUDE.md` before a model sees them. `sync` finds the block by those markers and replaces it in place, leaving the rest of the file untouched.
