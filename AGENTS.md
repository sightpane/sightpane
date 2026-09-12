# AGENTS.md

The working rules for this repo live in [CLAUDE.md](CLAUDE.md). Read it before changing or reviewing anything: it covers tenant isolation, the two price catalogs, seeded prompt text that ships in migrations, generated files, and environment traps.

## Code Review Rules

Review for defects that change behaviour, not style. Give every finding one label and start the comment with it in bold:

- **BLOCKER**: must not merge. The money or execution path, data loss, a security or tenant-isolation hole, or a change that defeats a guard the PR claims to add.
- **CRITICAL**: a real defect a user or the model will hit. A wrong number returned as a success, a false statement shown to users, or a claim of verification in the PR body that isn't true.
- **MEDIUM**: a real defect with a bounded blast radius, a behaviour change with no test pinning it, or a doc the change makes wrong.
- **NIT**: wording, consistency, a test that could pin more. No runtime effect.

Only report what you can point to in the code. If you can't show the mechanism, ask a question instead.
