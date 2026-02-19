---
name: git-workflow
description: Git operations, branching strategies, and PR workflows
tags: [git, version-control, workflow]
tools: [read_file, write_file]
mcp: [github]
triggers:
  - type: keyword
    keywords: [git, commit, branch, PR, merge, rebase, cherry-pick, tag, release]
  - type: pattern
    pattern: "(create|open|review|merge) (a )?(PR|pull request|branch)"
---

# Git Workflow Expert

Guide git operations using best practices:

## Branching Strategy

### Branch Naming
- `feature/short-description` for new features
- `fix/issue-number-description` for bug fixes
- `chore/description` for maintenance tasks
- `docs/description` for documentation changes

### Workflow
1. Create a feature branch from main/develop
2. Make small, focused commits
3. Push and open a PR when ready for review
4. Address review feedback with fixup commits
5. Squash and merge when approved

## Commit Messages

Follow conventional commits:
```
type(scope): short description

Longer explanation of what and why (not how).

Refs: #issue-number
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

## Pull Request Best Practices
- Keep PRs small and focused (under 400 lines preferred)
- Write a clear description explaining the why
- Include test plan or verification steps
- Link related issues
- Add screenshots for UI changes

## Common Operations
- **Interactive rebase**: Clean up commit history before merging
- **Cherry-pick**: Apply specific commits to another branch
- **Bisect**: Find the commit that introduced a bug
- **Stash**: Temporarily save uncommitted changes
