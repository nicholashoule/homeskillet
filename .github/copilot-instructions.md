# Copilot Instructions

This file provides repository-level context to GitHub Copilot and compatible AI coding agents.

## Project Instructions

[AGENTS.md](../AGENTS.md) is the canonical guide: the safety rules, Go conventions, and the
checks that must pass (`make check`). Follow it; this file adds only the conventions below.
Long-form context lives in [README.md](../README.md) and [docs/](../docs/), where
[docs/architecture.md](../docs/architecture.md) is the safety contract.

## Scoped Instructions

Put scoped rules in `.github/rules/` using the `*.md` naming convention, and set `applyTo` in the
YAML frontmatter (e.g. `applyTo: "**/*.go"`) to scope each file.

## File Constraints

**File Limits**: Markdown files must not exceed the line limits in `repogov-config.json`. Use `docs/` for detailed explanations.

**Handling the line limit (priority order):**

1. **Refactor First** - Remove redundancy, condense verbose explanations
2. **Move to `docs/`** - Relocate detailed content to `docs/` directory
3. **Link, Don't Repeat** - Reference external docs instead of duplicating
4. **Split Only When Necessary** - Only when content is a distinct concern; use cohesive files, descriptive names, and cross-references

Run checks: `go run github.com/nicholashoule/repogov/cmd/repogov@v0.9.0 -agent copilot all`
Re-scaffold missing files: `go run github.com/nicholashoule/repogov/cmd/repogov@v0.9.0 -agent copilot init`
Check for emoji: `go run github.com/nicholashoule/demojify-sanitize/cmd/demojify@v1.2.0 -root . -exts .go,.md,.yml,.yaml,.json`

repogov and demojify are pinned to releases, not `@latest`; bump each here, in `AGENTS.md`,
`docs/development.md`, `.github/workflows/ci.yml`, and `tools/githooks/pre-commit` together.

## File Naming Conventions

**Prefer lowercase filenames** in `docs/` and `.github/` directories:

- Use `kebab-case` or `snake_case` for multi-word filenames
- Exception: `*_AUDIT.md` files in `docs/` may use uppercase
- Exception: GitHub-mandated filenames remain uppercase (e.g., `CODEOWNERS`)
