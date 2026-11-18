# Configuration

`bellek` reads its settings from the environment only, never from a file, so the key Claude Code passes decides the workspace. Set them in the `env` of `~/.claude/settings.json`, next to `BELLEK_TOKEN`, or in a repository's untracked `.claude/settings.local.json`.

| Variable | Default | Meaning |
|---|---|---|
| `BELLEK_TOKEN` | none, required | An API key, which starts with `bk_` |
| `BELLEK_CAPTURE` | `prompts,files,progress,memory` | What the hooks record. Empty turns these four off; session start, location, the checkpoint note and session end still run, see [privacy.md](privacy.md) |
| `BELLEK_CAPTURE_IGNORE` | `.env*,*.pem,*.key,*lock*,id_rsa*,id_dsa*,id_ecdsa*,id_ed25519*,.netrc,.npmrc,.pypirc,credentials,*credentials.json,.git-credentials` | Path globs the hooks never record. Empty ignores nothing |
| `BELLEK_MEMORY_DIRS` | empty | Extra Claude Code memory folders to sync, comma separated |
| `BELLEK_RECALL` | `briefing,prompt,files` | What the hooks put in front of the model. Empty turns all three off |
| `BELLEK_BRIEF_TOKENS` | `800` | Size of the briefing at session start |
| `BELLEK_STATE_DIR` | `$XDG_STATE_HOME/bellek`, else `~/.local/state/bellek` | Where the hooks keep their state |
| `LOG_LEVEL` | `info` | Log level on stderr. `warn` stops the MCP bridge from logging every tool call |

`bellek config` prints the settings it reads, with the key masked.

[hooks.md](hooks.md) explains each hook and each setting, and [mcp.md](mcp.md) the MCP tools.
