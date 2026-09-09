# Vendored skill sources

Skills under this directory were originally written for another repository and adapted
to sightpane on 2026-09-09. Provenance of the vendored parts:

| Skill | Upstream | License |
|---|---|---|
| `spec-first-testing` (rationalisations table, red flags, mirror-assertion rule) | [obra/superpowers](https://github.com/obra/superpowers) `test-driven-development` @ `b36e082` | MIT © 2025 Jesse Vincent |
| `debugging-advanced` (red flags, rationalisations, root-cause-first rule) | [obra/superpowers](https://github.com/obra/superpowers) `systematic-debugging` @ `b36e082` | MIT © 2025 Jesse Vincent |
| `skill-creator` | Anthropic skill-creator (see its `LICENSE.txt`) | as stated there |
| `prompt-optimizer` | see its `SOURCES.md` and `LICENSE`; only the "Local note" block is ours | as stated there |
| `shadcn-flutter` (SKILL.md body, `guides/`, `components/`, `icons/`) | [sunarya-thito/shadcn_flutter](https://github.com/sunarya-thito/shadcn_flutter) `packages/shadcn_flutter/skills/shadcn-flutter` @ `a7c10e0` (2026-09-06, matches shadcn_flutter 0.0.54) | BSD-3-Clause © 2025 Thito Yalasatria Sunarya (`LICENSE` in the skill dir); only the frontmatter description and the "Local note" block are ours |
| `golang-pro` (whole directory) | [Jeffallan/claude-skills](https://github.com/Jeffallan/claude-skills) `skills/golang-pro` @ `882ef55` (2026-08-07) | MIT (`LICENSE` in the skill dir); vendored unchanged |
| `flutter-chart` (SKILL.md body, `references/`) | [entronad/graphic](https://github.com/entronad/graphic) `skills/flutter-chart` @ `3b6f31e` (2026-02-25, matches graphic 2.7.0) | MIT © 2024 LIN Chen (`LICENSE` in the skill dir); only the frontmatter description, the pub-cache path and the "Local note" block are ours |

To refresh either of the last two: clone the upstream repo, copy the skill directory over
ours, then re-apply the frontmatter and the trailing "Local note" section (diff first —
upstream may have added component files worth keeping).

Everything else (`code-auditor`, `pr-writer`, `issue-writer`, `pr-reviewer`, the local
notes) is repo-specific and maintained here.

Not vendored: the official Flutter/Dart skills (`dart-*`, `flutter-*`) and the Dart MCP
server come from the `dart-flutter@dart-flutter` plugin
([flutter/agent-plugins](https://github.com/flutter/agent-plugins), BSD-3-Clause),
enabled at project scope in `../settings.json`. They are pulled from the marketplace,
not copied here; update with `claude plugin marketplace update dart-flutter`.

`go-fiber` is ours, written against the Fiber v3 code in this repository — not vendored.

**Note on skillspot.dev/skills/community-go-fiber.** That listing advertises a
Fiber skill, but its install command downloads `Jeffallan/claude-skills`
`skills/golang-pro/SKILL.md`, which mentions Fiber nowhere, and it copies only
`SKILL.md` while that file's "Reference Guide" points at five `references/*.md`
files the command never fetches. So we vendored the whole `golang-pro` directory
(genuinely useful, general Go) and wrote our own `go-fiber` skill for the
framework part.
