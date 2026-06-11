# Documentation style guide

How documentation is written, organized, and published in this repository.

## Framework: Diátaxis

Every page belongs to exactly one of four types ([diataxis.fr](https://diataxis.fr)):

| Type | Answers | Reader mindset | Examples here |
| --- | --- | --- | --- |
| **Tutorial** | "Teach me" | Learning, hands-on | `getting-started.md`, `installation.md` |
| **How-to guide** | "How do I…?" | Working, goal-driven | `deployment.md`, `backups.md`, `runbook.md`, `troubleshooting.md` |
| **Reference** | "What is…?" | Looking something up | `api.md`, `configuration.md`, `scripts.md`, `versioning.md` |
| **Explanation** | "Why…?" | Understanding | `architecture.md`, `security.md`, `platform.md` |

When writing a new page, pick the type first. If a page mixes types (e.g. reference tables
inside a how-to), split it or cross-link instead.

## Page template

```markdown
# Page title

One-sentence summary of what this page covers and who it is for.

> **Type:** how-to · **Audience:** operator · **Last reviewed:** YYYY-MM-DD

## Section …
```

Rules:

- One `#` H1 per file, matching the sidebar entry.
- Start with a 1–2 sentence summary before any heading.
- Prefer tables for enumerable facts, prose for reasoning, fenced code blocks for every command.
- Mermaid diagrams for flows; keep them under ~20 nodes.
- Commands must be copy-pasteable: include working directory if it matters, no `$` prompt prefix.

## Linking

- **In-repo links**: relative markdown links — `[Architecture](architecture.md)`.
  Never absolute GitHub URLs for in-repo files (break in wiki and forks).
- **Wiki**: pages are mirrored from `docs/*.md` by `release.sh`; relative `.md` links are
  rewritten to wiki page names automatically. Do not hand-write `[[WikiLinks]]` in `docs/`.
- **Anchors**: lowercase, hyphenated (`architecture.md#control-plane`).

## File placement

| Location | Content | Published to wiki |
| --- | --- | --- |
| `docs/*.md` | Current guides and reference | ✅ |
| `docs/changelog/` | Per-release notes (`vX.Y.md`) | ✅ (as `Changelog-vX.Y`) |
| `docs/roadmap/` | Forward-looking plans | ❌ |
| `docs/archive/` | Superseded plans, kept for history | ❌ |
| `wiki-staging/` | Wiki-only meta pages (`Home`, `_Sidebar`, `_Footer`) | ✅ |

A page that becomes obsolete moves to `docs/archive/` with a one-line banner at the top:
`> **Archived YYYY-MM-DD** — superseded by [X](../<page>.md).`

## Lifecycle

1. New/changed feature → update the relevant page **in the same PR** as the code.
2. Update `docs/index.md` and `wiki-staging/_Sidebar.md` when adding or removing a page.
3. Run the link checker before release: `bin/docs-check.sh`.
4. The GitHub Wiki is write-only output of `release.sh` — never edit it directly.

## Review checklist

- [ ] Page has a type and fits it
- [ ] Summary sentence present, H1 matches sidebar
- [ ] Commands tested or copied from a tested run
- [ ] No secrets, hostnames, or tokens (release scrub-gate also checks)
- [ ] `bin/docs-check.sh` passes
