---
name: git-workflow
description: Use when making repository changes or managing branches, Git worktrees, pull requests, merges, or post-merge cleanup in the go-software-factory repository.
---

# Git workflow

## Default workflow

1. For a new feature, fix, or other change, use a new `codex/<task-name>` branch from local `main` and a dedicated worktree at `<primary-repository-root>/.worktree/<task-name>`. Identify the primary checkout with `git worktree list --porcelain`; use its root even when running from another linked worktree. Preserve existing changes in other checkouts.
2. Before creating the worktree, ensure `/.worktree/` is ignored; verify with `git -C <primary-repository-root> check-ignore .worktree/<task-name>`. If missing, add that entry to the common Git directory's `info/exclude` as local setup and verify again. Create the branch and worktree before implementation edits. Use a tool that supports this location, or `git worktree add` if the native worktree tool cannot select it.
3. Implement the requested changes and run applicable checks inside the task worktree. Commit, push the branch, and create a pull request against `main` or the user-specified target branch.
4. Keep the branch and worktree during user review. Continue an existing PR or apply requested follow-up changes in its existing branch and worktree. The user merges the PR, or explicitly asks the agent to merge it; review approval alone does not authorize merging.
5. Confirm that the PR is merged before cleanup. Leave the task worktree, remove it, and delete the task's local and remote branches if they still exist. Clean up only resources created for this task. If the worktree is dirty, preserve it and its branch and report pending cleanup; do not force removal or discard unrelated work.

## Exceptions

- Requests limited to recording or editing backlog entries or updating `AGENTS.md` may be performed directly on `main` without a new branch, worktree, or PR. Implementing a backlog task follows the default workflow.
- Explicit requests to work on `main` or another specific branch take precedence: use that branch instead of creating a new one. Honor a user-specified base branch or worktree location. Do not delete a user-specified branch or worktree during automatic cleanup.
