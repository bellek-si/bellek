# What leaves your machine

`bellek` sends data only to `BELLEK_URL`, `https://bellek.si` unless you set it, with your key in the `Authorization` header and nowhere else. A redirect to another host does not carry the key. Every request also names the client release in the `User-Agent` header, as `bellek/<version>`, so the server can log which release called it. A plain `http://` address on another machine would send the key unencrypted, so every command warns about it on stderr. Every text field the hooks send goes through `internal/redact` first, see [hooks.md](hooks.md#what-is-captured-and-what-is-not) for what it removes, and anything between `<private>` and `</private>` never leaves.

## The hooks

| Hook | Request | What it carries |
|---|---|---|
| `session-start` | `POST /v1/sessions` | The repository address, such as `github.com/acme/app`, which carries no credentials, the `repoId` when the repository has one, and `repoName` when it has no address, see below, the branch, `agentKind` `claudeCode`, your hostname as `agentId`, the Claude Code session id when it is a uuid, and the briefing budget |
| `session-start` on a reused session | `GET /v1/briefing` | The Bellek session id and the briefing budget |
| `session-start` | `POST /v1/notes` | One note per Claude Code memory file that changed since the last start, see [Memory sync](hooks.md#memory-sync) |
| `prompt` | `POST /v1/sessions/:id/prompt` | Your prompt, cut at 16 KiB, and the ids of the signals the last prompt showed |
| `pre-tool-use` | none | Saves the size and time of the files `git status` lists, on your machine only |
| `tool-use` | `POST /v1/sessions/:id/artifacts` | The paths of the files the tool wrote, relative to the repository. Never their contents. Paths matching `BELLEK_CAPTURE_IGNORE` are left out |
| `tool-use` on a memory file | `POST /v1/notes` | That memory file as a note |
| `stop` | `POST /v1/sessions/:id/progress` | The closing message of the turn, cut at 16 KiB, and the name Claude Code gave the session when it changed |
| `prompt` in a repository that had no remote | `POST /v1/sessions/:id/location` | Once the repository has a remote: its address, the `repoId` and the branch |
| `cwd` | `POST /v1/sessions/:id/location` | The repository address, the `repoId` when the repository has one, and the branch of the new directory |
| `pre-compact` | `POST /v1/notes` | A checkpoint note: the trigger, the time, the branch and `git diff --stat` |
| `session-end` | `POST /v1/sessions/:id/progress` | `git diff --stat` of the checkout, when it shows changes, and the name Claude Code gave the session when it changed |
| `session-end` | `POST /v1/sessions/:id/end` | Why the session ended, such as `clear` or `logout` |

`BELLEK_CAPTURE` turns the prompt, file, progress and memory requests off one by one; the session start, location, checkpoint and end requests always run. A prompt, file, progress, checkpoint or memory request that fails waits in the spool on your disk, `BELLEK_STATE_DIR/spool`, for at most seven days, and a later hook sends it.

A git repository without a remote is recorded too, and there is no setting to stop it. The hooks or the bridge write a random uuid to its git config as `bellek.repoId` and send it as `repoId`, with `repoName`, the folder name of its main worktree, redacted. No other part of the path is sent. A folder outside any git repository is not recorded.

Never sent: the contents of any file other than a Claude Code memory file, tool output, command lines, and the transcript. The hooks read the last 64 KiB of the transcript for the session name alone.

## `bellek mcp`

The MCP bridge forwards the tool calls the model makes to `/mcp`, with the arguments the model chose, as the model wrote them. It adds the repository address and the branch to a call when the tool takes them and the model left them out, and both go through `internal/redact` like the location a hook sends. It also adds the `repoId` the repository holds, writing one first in a repository without a remote, and there, on `session_start`, the redacted `repoName`. It adds neither when the model named a `projectKey` or a `repoRemote`. A remote the model wrote is turned into its address too, so it never carries credentials.

## The other commands

`bellek doctor` reads `/v1/health`, `/v1/me` and, for an admin, `/v1/doctor`. `bellek export` reads a project and writes it to your disk. `bellek import` sends the markdown files you name: a Claude Code memory file goes through `internal/redact` as in memory sync, and a plan or Bellek markdown file goes as written. None of them sends anything else.

## Who reads it

On the server, only you read your prompts, closing messages and session names. Others in the project, admins included, see that the session exists, the task it holds and the files it touched. A decision or a task report starts shared with the project. Any other note, and every synced Claude Code memory, starts private to you and the admins above you.
