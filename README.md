# Bellek

Shared memory for Claude Code sessions. Bellek keeps the tasks, decisions and notes your coding sessions share. A new session starts with a briefing of where the work stands, two sessions never work on the same task, and a session that stops leaves a handoff for the next one.

This repository holds what runs on your machine:

- `bellek`, the command the hooks and the MCP server run, plus `doctor`, `export` and `import`
- the Claude Code plugin: eight hooks, the Bellek MCP tools and a skill that tells the model when to read and write
- the hook settings, for setups without the plugin

The server is the hosted service at [bellek.si](https://bellek.si). The client talks to it over HTTPS with an API key, and you can read here exactly what it sends: see [docs/privacy.md](docs/privacy.md).

## Quick start

### 1. Install `bellek`

On macOS, with Homebrew:

```bash
brew install bellek-si/tap/bellek
```

On Linux, download the latest release for your machine, `amd64` or `arm64`, and check it:

```bash
arch=amd64
curl -fsSLO "https://github.com/bellek-si/bellek/releases/latest/download/bellek-linux-$arch.tar.gz"
curl -fsSLO "https://github.com/bellek-si/bellek/releases/latest/download/checksums.txt"
sha256sum --ignore-missing -c checksums.txt
tar -xzf "bellek-linux-$arch.tar.gz" bellek
sudo install -m 755 bellek /usr/local/bin/bellek
```

With Go, the same binary builds from source:

```bash
go install github.com/bellek-si/bellek/cmd/bellek@latest
```

Keep the folder on your `PATH`: the hooks and the MCP server call `bellek` by name.

### 2. Add your API key

Sign in at [bellek.si](https://bellek.si) and create a key on the API keys page under Settings. The page shows the key once, inside a block to paste into the `env` of `~/.claude/settings.json`:

```json
{
  "env": {
    "BELLEK_TOKEN": "bk_..."
  }
}
```

The file now holds your key, so keep it readable by you alone: `chmod 600 ~/.claude/settings.json`. A key belongs to you in one workspace. For a repository that works in another workspace, put that workspace's key in the repository's untracked `.claude/settings.local.json` instead.

### 3. Install the plugin

```bash
claude plugin marketplace add bellek-si/bellek
claude plugin install bellek@bellek --scope user
```

Start Claude Code in a git repository: each session starts with the Bellek briefing in context. To check the setup from a shell, export `BELLEK_TOKEN` and run:

```bash
bellek doctor
```

It names the key, its user and its workspace, and the client and server versions.

## Commands

| Command | What it does |
|---|---|
| `bellek hook <event>` | The Claude Code hook entry point. Reads the event JSON on stdin. The plugin runs it, you do not |
| `bellek mcp` | Serves the Bellek MCP tools over stdio by forwarding them to the server |
| `bellek doctor` | Checks the server, the key and the versions. For a workspace admin, also prints the database counts |
| `bellek export --project K --out DIR` | Writes a project to markdown files |
| `bellek export --project K --out DIR --report KEY` | Writes the report on one task, or with `--kind session` one session |
| `bellek import --project K --from PATH` | Reads markdown files or a plan file into a project |
| `bellek config` | Prints the settings it reads, with the key masked |
| `bellek version` | Prints the version |

Commands print their output on stdout and log on stderr. [docs/markdown.md](docs/markdown.md) describes the export and import formats.

## Configuration

`bellek` reads its settings from the environment only, never from a file, so the key Claude Code passes decides the workspace.

| Variable | Default | Meaning |
|---|---|---|
| `BELLEK_TOKEN` | none, required | An API key, which starts with `bk_` |
| `BELLEK_CAPTURE` | `prompts,files,progress,memory` | What the hooks record. Empty turns these four off; session start, location, the checkpoint note and session end still run, see [docs/privacy.md](docs/privacy.md) |
| `BELLEK_CAPTURE_IGNORE` | `.env*,*.pem,*.key,*lock*,id_rsa*,id_dsa*,id_ecdsa*,id_ed25519*,.netrc,.npmrc,.pypirc,credentials,*credentials.json,.git-credentials` | Path globs the hooks never record. Empty ignores nothing |
| `BELLEK_MEMORY_DIRS` | empty | Extra Claude Code memory folders to sync, comma separated |
| `BELLEK_RECALL` | `briefing,prompt,files` | What the hooks put in front of the model. Empty turns all three off |
| `BELLEK_BRIEF_TOKENS` | `800` | Size of the briefing at session start |
| `BELLEK_STATE_DIR` | `$XDG_STATE_HOME/bellek`, else `~/.local/state/bellek` | Where the hooks keep their state |
| `LOG_LEVEL` | `info` | Log level on stderr. `warn` stops the MCP bridge from logging every tool call |

[docs/hooks.md](docs/hooks.md) explains each hook and each setting, and [docs/mcp.md](docs/mcp.md) the MCP tools.

## Development

You need Go, the version in `go.mod`.

```bash
make test     # tests with the race detector
make lint     # gofmt and go vet, or golangci-lint when installed
make build    # dist/bellek for this machine
make install  # copy it to /usr/local/bin
```

To try a changed plugin, run `make plugin-install` in your checkout, and `make plugin-update` after each change under `plugin/`. [CONTRIBUTING.md](CONTRIBUTING.md) has the rest.

Other Go code can import `api`, the types `/v1` and `/mcp` exchange, and `client`, the HTTP client. Their Go API may change in any v0 release, so pin a version.

## Security

Report a vulnerability to info@bellek.si, see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
