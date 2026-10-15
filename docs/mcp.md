# MCP

Bellek serves its twelve operations as MCP tools over HTTP at `/mcp`. `bellek mcp` bridges stdio to that route for agents that speak stdio.

## Claude Code over stdio

The Bellek plugin installs the server together with the hooks, see [hooks.md](hooks.md). Without the plugin, add a `.mcp.json` to the project you are working in:

```json
{
  "mcpServers": {
    "bellek": {
      "command": "/absolute/path/to/bellek",
      "args": ["mcp"]
    }
  }
}
```

The file holds no key, because a project's `.mcp.json` is often committed. `bellek mcp` reads `BELLEK_TOKEN`, an API key, and `BELLEK_URL`, default `https://bellek.si`, from the environment only, usually the `env` block of Claude Code's settings, see [the README](../README.md#2-add-your-api-key).

`bellek mcp` connects to `BELLEK_URL/mcp` with the key, mirrors the tools and the instructions the server offers and forwards every call, so new tools and instructions need no new client. It sends the key to the host of `BELLEK_URL` only, never to a host a redirect names. It logs to stderr, so stdout stays clean for the protocol: at `info`, one `mcp call` line per forwarded call, with the tool name, `durationMs` and the error when there is one. Claude Code shows that stderr only in its debug log, so run `claude --debug` to read it.

The bridge fills `cwd`, `repoRemote`, `repoRoot` and `branch`, each one only when the tool takes it and the caller left it out. They come from the directory the bridge was started in, or, when the caller passes a `cwd` of its own, from that directory, read with git within one second. Four tools take a project: `session_start`, `memory_get`, `memory_search` and `task_next`; the rest use the project of `sessionId`. `repoRoot` is the main worktree root, the first entry of `git worktree list`. The server finds the project by `projectKey`, then `repoRemote`, then `repoRoot`, else `cwd`, so a `cwd` in another repository resolves to that repository's project. A moved checkout keeps its project through the remote, and its new root joins `repoPaths`. After a repository rename, only a checkout whose root is already saved finds the project; a clone at another path starts a new one. A workspace admin avoids that by setting the new remote on the project's settings page at [bellek.si](https://bellek.si).

## HTTP

`POST /mcp` on the API port, with an API key as for the API. A key acts as its user in its workspace. Call it directly when you do not want the stdio bridge. It runs in stateless JSON response mode: one JSON body per request, no event streams, no `initialize` needed. Send `Accept: application/json, text/event-stream`, or the request is refused with 400. Nothing fills `cwd`, `repoRemote`, `repoRoot` or `branch` here, so pass `projectKey` or `repoRemote` yourself. At `info` the server logs one `mcp call` line per tool call, with the error code when the call fails.

```bash
curl -s -X POST "https://bellek.si/mcp" \
  -H @<(printf 'Authorization: Bearer %s\n' "$BELLEK_TOKEN") \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

## Tools

| Tool | Read only hint | Purpose |
|---|---|---|
| `session_start` | no | Start a session, resolve or create the project, return the briefing |
| `session_end` | no | End the session, write a handoff per claimed task, release leases |
| `memory_briefing` | yes | The session's intent and files, claims, blocked tasks, unread signals, handoffs, decisions, global decisions, frontier, other sessions. Shows the newest three unread signals and marks them read, and renews its leases |
| `memory_get` | yes | One node with neighbors and recent events, optional `asOf` |
| `memory_search` | yes | Full text hits in the project, then in every scope you read, plus one hop of related nodes. The markdown marks a scope hit as `(kind, <scope key>)`, such as `(decision, team-a)`. `kinds` `["session"]` finds who worked on something |
| `memory_note` | no | Write a note or decision, linked to related work automatically. `projectKey` `global` writes your own rule for every repository; `team-<team key>` or `org` writes a team or org rule, which a reader who may not write there sends with the tag `proposed` for an admin to accept. A new decision or report is shared with everyone in the project; any other new note is private to you and the admins above you, and `visibility` `shared` gives it to everyone |
| `memory_signal` | no | Message another session by id or session key, at most 200 characters. Sent to a task key, it is only an event on the task's timeline and reaches no session |
| `task_upsert` | no | Create or update a task |
| `task_status` | no | Move a task through the state machine. Its description asks for the report note at `done` or `review` |
| `task_claim` | no | Claim, release or renew a lease. A claim makes the task the session's focus and returns its briefing |
| `task_handoff` | no | Manual handoff with summary, open questions, next steps, files touched |
| `task_next` | yes | Unblocked, unclaimed tasks by priority |

[`api/tools.go`](../api/tools.go) defines the tools, their arguments and the instructions the server sends. Change them there.

Field names are camelCase. Fields, defaults and rules match the `/v1` HTTP routes, whose shapes are in the [`api`](../api) package, except:

- A key or id the route takes in its path is a field: `key` on `memory_get`, `task_upsert`, `task_status`, `task_claim` and `task_handoff`, and `sessionId` on `session_end`.
- `memory_get` reads a `depth` of 0 as 1. The route returns the node without neighbors for 0.
- `memory_get`, `memory_search` and `task_next` also take `repoRoot`, for the bridge to fill.
- `memory_get` takes no `author`, and `memory_note` no `authorId`: an agent reads and writes its own note under a key, else the shared one.
- `peek` is HTTP only, because a briefing that marks nothing read and renews no leases is the UI's read path, not an agent's.

A task field, `key`, `parentKey`, `dependsOn`, `relatedKey`, `to` and a handoff's `taskKey`, takes the task's id, such as `widgets-12`, or its key. The markdown names a task by its id then its key, `widgets-12 auth-refresh`, and the server instructions ask the model to name tasks to the user by id.

A successful result carries two text blocks: a short markdown summary and the JSON payload. For a claim the markdown block also carries the task briefing.

Leave `baseVersion` on `memory_note` and `task_upsert` empty. It is for the browser's editor and verdict buttons, which show the newer version when the server refuses a stale write with `conflict`.

## Suggested flow

The server instructions give every client a short form of this flow. With the hooks installed, the session is already started and its briefing, with the session id, is already in context:

1. `memory_search` or `memory_get` before touching a task or a file.
2. `task_upsert` for each piece of work the user asks for or you pull from a tracker such as Linear or Jira, with `parentKey` and `dependsOn` for related work. Then `task_claim` it and move it to `inProgress` with `task_status`, before your first edit, even for a small one line request. The claim returns the task's briefing, and from then on the session's prompts, edits and notes are recorded against the task until it is released, handed off or finished. A claim leaves the status alone, from `todo` a task can only go to `inProgress` or `cancelled`, and a move to the status it already has changes nothing and is not refused. Then `task_status` as it moves; `done` or `review` on the task you hold writes the handoff for you when you wrote none.
3. `memory_note` for decisions, lessons and open questions. Without `relatedKey`, the note relates to the task you hold. When you move a task to `done` or `review`, also write a `memory_note` tagged `report` with key `<task key>-report`, `relatedKey` the task and the headings `## What`, `## Why`, `## Changes` and `## Conclusion`. The report on the task reads it.
4. `task_handoff` if you stop mid task.

Other sessions are in the briefing's active sessions section with their key, name and focus. Reach a live Claude Code peer on the same machine with Claude Code's own `SendMessage` by its name, and any other session, on any machine or harness, with `memory_signal` to its session key. The target sees the signal on its next prompt when it runs the hooks, see [hooks.md](hooks.md#what-the-model-sees), or in its next briefing. Answer a `request` with kind `response`, the sender's key as `to` and the same `threadId`.

Without the hooks, call `session_start` with `agentKind` and `agentId` first and keep the session id it returns. Call `memory_briefing` when you need to know where things stand, and `session_end` when you stop. The server ends a session that has not written or read its briefing for a day.

## Errors

A failed call returns an error result whose text starts with a code and a colon, such as `notFound: project not found`. The codes are `validation`, `noRepository`, `notFound`, `sessionEnded`, `leaseHeld`, `conflict`, `invalidTransition`, `paymentRequired` and `internal`, the same codes as the HTTP API. `paymentRequired` is a Free workspace at its cap refusing a new task, note or decision from `task_upsert` or `memory_note`: `paymentRequired: Free holds 100 records, upgrade at <url>`. Editing an existing key, `task_status`, `task_claim`, `task_handoff`, `memory_briefing` and the rest keep working, and the briefing then carries the line "Bellek's free plan is full, so new tasks, notes and decisions are not saved." Branch on the prefix. An `internal` error reads `internal: internal error`; the server logs the real one. There is no `badRequest`: a `sessionId` that is not a uuid or a time that is not RFC3339 arrives as `validation`. Arguments that break the tool's input schema, such as a missing required field or a string where a number belongs, are refused before Bellek sees them, with text that starts `validating "arguments"` instead.
