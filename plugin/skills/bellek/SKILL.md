---
name: bellek
description: Bellek is the shared memory of this project. Use it before touching a task or a file, when asked where things stand, what was decided, or what another session did, and at the end of a turn to record a decision, a lesson, an open question or a task status change. The briefing is already in context at session start; search with memory_search and memory_get, record with memory_note and task_status.
---

# Bellek

The Bellek hooks run on their own. They start the session, record every prompt, every file an edit tool changes and every closing message, and end the session when Claude Code does. The briefing arrives with the session start context, prefixed "Bellek briefing for session". The session id in that prefix and in the per prompt context line is the `sessionId` a tool asks for.

## Track the work

- When the user asks for a piece of work, or you pull tasks from a tracker such as Linear or Jira, create each one with `task_upsert`: a short `key`, the title, the body with what done means, `parentKey` for a piece of a larger task and `dependsOn` for work that must land first.
- Every task has an id, the project key and a number such as `widgets-12`. Name tasks to the user by id. Any task field, `key`, `parentKey`, `dependsOn`, `relatedKey`, takes the id or the key. A new key must not look like an id of this project.
- `task_claim` the one you start, with `action` `claim`. The result is its briefing, and until you let go your prompts, edits and notes are recorded against it. A claim leaves the status alone, so move a `todo` task to `inProgress` with `task_status` next: from `todo` it can only go to `inProgress` or `cancelled`. A move to the status the task already has changes nothing and is not refused.
- Create, claim and start the task before your first edit, even for a small one line request. A task written after the work was never claimed, so nothing you did is recorded against it.
- Move it with `task_status`: `done` when finished, `review` when it needs a look, `blocked` with the reason when something outside your control stops it, `cancelled` when dropped. A task whose dependency is still open stays out of the frontier on its own.
- When you move a task to `done` or `review`, write a `memory_note` tagged `report` with key `<task key>-report` and `relatedKey` the task, one or two paragraphs under each of `## What`, `## Why`, `## Changes` and `## Conclusion`. The report page on the task reads it; a missing section falls back to what Bellek recorded.

## Read before you act

- `memory_search` with `q` before touching a task or a file. It searches this project, then every scope you read: your own `global`, your teams' `team-<key>` and the `org`, whose hits read `(kind, <scope>)`. Each hit brings up to three neighbors: tasks first, then decisions, notes and, behind a task or session hit, files.
- `memory_get` with a `key`, and its `kind` unless it is a task, for one task, note, decision, session or artifact with its neighbors and recent events.
- `memory_briefing` only when the briefing from session start is gone from context.
- `task_next` for unblocked, unclaimed tasks by priority.
- `memory_search` with `kinds` `["session"]` to find who worked on something; a session's body is its summary and last progress message, and `memory_get` on it lists the files it touched. Another user's session shows no text, and a search matches only your own.

## Reach another session

- The briefing's active sessions section lists the other agent sessions seen within the lease TTL, with key, agent id, branch and focus.
- A live Claude Code peer on this machine is reached with Claude Code's own `SendMessage` by its name, which the briefing shows next to its key.
- Any other session, on any machine or harness, is reached with `memory_signal` to its session key, in at most 200 characters. A signal of any kind arrives in that session's context on its next prompt, at most three per prompt and each cut at 200 characters, or in its next briefing. Sent to a task key, a signal only joins the task's timeline and reaches no session.
- When a prompt carries "Signal from session ... request:", answer with `memory_signal` kind `response`, the sender's key as `to` and the same `threadId`.

## Record before you answer

- `memory_note` with `kind` `decision` for a decision, or a note tagged `lesson` or `question`. Add `relatedKey` for the task it belongs to; while you hold a task it is the default. Add `projectKey` `global` only for your own rule that holds in every repository, not just this one. `team-<key>` or `org` with the tag `proposed` offers a rule to a team or the whole org; an admin of that scope accepts it before any briefing reads it. A new decision or report is shared with everyone in the project; any other new note is private to the user and their admins, and `visibility` `shared` gives it to everyone. The user may later accept, discard, answer or promote what you wrote, and a discarded note drops out of search and the briefing.
- A Claude Code memory file of type `user` or `feedback` reaches your `global`, and so every repository's briefing. When it is about this repository alone, add `scope: project` under `metadata`, next to `type`, and it stays in this project.
- `task_status` to move the task you hold: `done` or `review` writes your handoff when you wrote none, and that handoff lets go of the task.
- `task_handoff` with summary, open questions, next steps and files touched when stopping mid task. It lets go of the task too.
- `task_claim` with `action` `release` only to drop a task without finishing it or handing it off.
- `memory_signal` to message another session or attach a message to a task, see above.

`session_start` and `session_end` exist for clients without the hooks. Do not call them from Claude Code; the hooks start and end the session.
