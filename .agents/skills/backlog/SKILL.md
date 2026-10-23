---
name: backlog
description: Use when the user asks to add, update, remove, or review tasks in the repository backlog, or to execute the backlog or a specific backlog task.
---

# Backlog

Manage user-requested repository tasks in `docs/backlog.md`, relative to the repository root. The user decides what enters the backlog and when it is executed. Recording a task does not authorize implementing it.

## Record a task

1. Read `docs/backlog.md`; create it if missing.
2. Add an unchecked task with a clear title, the problem or desired outcome, and relevant file paths or context needed to resume later. Record only known details; do not invent requirements.
3. Update an existing matching entry instead of duplicating it. Preserve unrelated entries.
4. Confirm briefly what was recorded and where. Do not implement the deferred task, make preparatory code changes, or start a background job to work on it. Continue only any separately authorized current work.

Use a compact entry, for example:

```markdown
- [ ] Add retry status to the process details page.
  Context: Show why a failed step is waiting for another attempt. Deferred from the current scheduler work.
```

Add tasks only when the user asks. Do not add agent-discovered improvements automatically. When the user asks to update or remove a task, change only the matching entry.

## Execute the backlog

1. Wait for an explicit request to execute or implement backlog work. Reviewing or discussing the backlog does not authorize implementation.
2. Read `docs/backlog.md`. If the user asks to execute the backlog, work through its unchecked tasks in listed order unless the user specifies another order or scope. If the user names a task or subset, execute only that selection. Ask which task only when the reference is ambiguous.
3. Recheck the current code and relevant repository instructions before implementing each task. Follow the recorded requirements without adding scope.
4. Mark a task `[x]` only after implementation and verification. Keep unfinished tasks unchecked, recording any blocker or remaining work so they can be resumed later.
5. Report what was completed and what remains.
