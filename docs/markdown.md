# Markdown import and export

Bellek keeps the database as the source of truth. Markdown is a way in and a way out.

## Export

```bash
bellek export --project bellek --out ./export
```

Reads the project a page at a time, oldest first, and writes one file per node under `<out>/<kind>s/<key>.md`: `tasks/`, `sessions/`, `notes/`, `decisions/`, `artifacts/`. An artifact key is a repository relative path, so its file sits in the directories that path names. The kind and the key come from the server's answer, so export stops with an error at a kind it does not know or a key whose file would land outside its kind directory. Files are written readable by you alone, `0600` in `0700` directories, because they hold the same captured prompts and notes as the server. Export holds only what you can read, so no one else's session text and no private note you cannot read. Each file has YAML front matter, the body, and for tasks a timeline after a marker line.

```markdown
---
kind: task
key: auth-refresh
title: Refresh auth
status: inProgress
tags:
    - auth
props:
    priority: 2
links:
    - type: dependsOn
      to: db-migrate
      kind: task
version: 3
createdAt: "2026-09-16T10:00:00Z"
updatedAt: "2026-09-16T11:00:00Z"
---

Rotate tokens.

<!-- bellek:timeline -->
- 2026-09-16T10:00:00Z claimed auth-refresh
```

A note or decision also carries `visibility`, `private` or `shared`. `links` are the node's outgoing edges other than the project link, `supersedes` and a live lease. A link without `kind` is a forward reference that has not been resolved yet. The timeline is the task's last 50 events, newest first. Import ignores the timeline, `version`, `createdAt` and `updatedAt`.

Export stops at the first node whose body contains the marker, and the files written before it stay. Import would cut that body at the marker and supersede the node with what is left.

```bash
bellek export --project bellek --out ./export --report auth-refresh
bellek export --project bellek --out ./export --report session-7b3a --kind session
```

`--report` writes only the report on that task or session to `<out>/<key>-report.md`, the name the UI's download uses. `--kind` is `task`, the default, or `session`, and is an error without `--report`. Like the full export, the file is `0600` in a `0700` directory, since a report holds captured text too. It has no front matter, so `bellek import` fails on a directory that holds one; write reports outside a directory you will import.

## Import

```bash
bellek import --project bellek --from ./export
bellek import --project bellek --from docs/superpowers/plans/2026-04-15-something.md
```

`--from` is a single file or a directory. In a directory every `.md` file is read, subdirectories included, except `MEMORY.md`. Three formats are accepted, and a file in none of them fails the import:

- **Bellek files** as written by export. `kind` must be `task`, `note` or `decision`; sessions and artifacts are skipped. A task without `status` is `todo`.
- **Claude Code memory files** with `name` and `metadata.type` in the front matter. They become notes, or decisions when the type is `feedback`, with the mapping in [hooks.md](hooks.md#memory-sync), except that every memory goes to `--project` and `metadata.scope` is ignored. A `metadata.modified` that is not RFC3339 fails the import.
- **Plan files** with `### Task N: Title` headings, where `N` is a number such as `3` or `2.1`, and `- [ ]` steps. A file that opens with front matter is never a plan. Each heading becomes a task keyed by the title slug, cut to 40 characters. The status is `todo` when no step is checked, `done` when all are, `inProgress` otherwise. The text up to the next heading of level one to three becomes the body.

`bellek import` sends the documents in batches of at most 200 documents and 768 KiB, under the server's 1 MiB request limit. Each batch runs in one transaction: nothing in a batch is written when any of its documents fails, and a failed batch stops the import, leaving the batches before it in place. A link into a later batch is a forward reference until that batch creates its target. Import creates the project if it does not exist, unless the key names a scope, and creates new nodes with origin `import`. Into a scope it needs someone who writes it and takes no tasks. An existing node gets a new version, also with origin `import`, when the file changes its title, body or status, adds a tag, or changes a prop the file names. Everything else counts as unchanged. Props the file does not name are kept. A new note or decision without `visibility` follows the rule of a note write: a decision or a note tagged `report` is shared, any other note and a synced Claude Code memory private. An existing one of yours takes the file's `visibility` when it has one, and someone else's keeps its own. Tags merge as on a note write: a verdict in the file replaces the node's verdict, and every other tag the node has stays, so import adds tags and never removes one.

Import sets a task's status directly, outside the state machine, and writes a `statusChanged` event when the status differs from the previous one, `todo` for a new task.

Links are applied for every document, including the unchanged ones, so a link added to a file lands without a new version. A link resolves by key to its `kind`, or without `kind` to a task, note or decision, in that order. A link to a task, or one without `kind`, may name the task's id, such as `widgets-12`, instead of its key. A new task whose key reads as an id of the project fails the batch, and so does such a link when no task has that id. An unknown target becomes a forward reference, resolved when a node with that key appears: a task ends any link, a note or decision only a `relatesTo` link, and no other kind ends one. The `kind` the link names is not kept, so a `relatesTo` link resolves to the first task, note or decision created with that key. New and changed nodes are linked to related work like agent writes.

The report line reads `created N, updated N, unchanged N, links N`, summed over the batches. `links` counts every link in the files, new or not. A failed batch still prints the line for the batches before it. On a full Free workspace the server leaves out each new document the cap refuses and applies the rest of the batch; the line then ends `, left out N` and the command exits non zero.

## Round trip

Export then import of the same project reports zero created and zero updated, except in one case: a body that ends with a newline comes back without it, so its node counts as updated once. The next round trip reports it unchanged.

Importing a plan file, exporting, and importing the export again reports zero changes.

## Doctor

```bash
bellek doctor
```

```text
server: ok (https://bellek.si)
versions: client v0.1.0, server v1.3.0
key: laptop (bk_Xq3vL9aZ)
user: Ada Lovelace <ada@example.com>
workspace: Acme (organization)
nodes: 412, edges: 1033, events: 2871
orphan edges: 0
unresolved forward references: 3
expired leases: 1
stale sessions: 0
largest project: bellek with 2604 events
retainable events: 0
```

Checks that the server and its database answer and prints the client and server versions, `unknown` for a server that sends none. When the client is older than the oldest version the server supports, the next line says to upgrade it; a `dev` build is never too old. It then names the key in `BELLEK_TOKEN`, its user and its workspace from `GET /v1/me`, and prints the counts of `GET /v1/doctor` for that workspace: nodes, edges and events, edges whose node is gone, links still waiting for their target, leases past their time, active sessions with no update for a day, the largest project and the events a retention job could drop. A member who is not an admin of the workspace gets `counts: only an admin of the workspace reads them` instead of the counts. A key the server refuses exits with "the server refused BELLEK_TOKEN: the key is unknown, revoked or expired". `largest project: none` means no event exists. When a project passes 500,000 events, half the retention trigger, one more line follows: `project <key> has N events, retention trigger is one million`. After printing all of it, the command exits with "doctor found problems" when the client is too old, also for a member who is not an admin, or when an orphan edge exists, never for that warning.
