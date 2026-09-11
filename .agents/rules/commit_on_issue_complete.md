---
trigger: always_on
description: Automatically create a descriptive Git commit whenever an issue, bug, or feature task is completed.
---

# Commit on Issue / Task Completion

Whenever an issue, bug fix, feature, audit finding, or user-requested task is finished:
1. **Verify First**: Ensure the implementation is complete, static checks (`go vet`, `golangci-lint`, etc.) pass, and relevant automated tests pass with no regressions.
2. **Commit Automatically**:
   - Stage the modified and new files relevant to the completed task (`git add <files>`).
   - Do NOT stage unrelated changes or temporary/scratch files.
   - Create a commit with a clear, conventional commit title and a detailed message body explaining the changes:
     ```bash
     git commit -m "<type>(<scope>): <summary>" -m "<detailed bullet points explaining what was changed and why>"
     ```
   - Types: `fix`, `feat`, `refactor`, `perf`, `test`, `docs`, `chore`.
   - If the task addresses an issue, mention it in the commit footer (e.g., `Fixes #123` or `Closes #123`).
3. **Do Not Push**: Do not run `git push` unless the user explicitly asks to push.
