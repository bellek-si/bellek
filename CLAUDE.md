# Bellek client

This repository holds the `bellek` command, the Claude Code plugin and the hook settings. They talk to the Bellek service at https://bellek.si only through the `/v1` HTTP API and `/mcp`.

## Commands

- `make test` runs the tests with the race detector.
- `make lint` checks gofmt, then runs golangci-lint when installed, else `go vet`.
- `make ci` runs what `.github/workflows/ci.yaml` runs, and `make release` what `release.yaml` runs after `make github-release`.
- `make build` writes `dist/bellek-<os>-<arch>` and links `dist/bellek` to it. `make install` copies it to `INSTALL_DIR`, default `/usr/local/bin`, with sudo only when that directory is not writable.
- `make plugin-install` registers this checkout as the `bellek` marketplace in `CLAUDE_CONFIG_DIR` and installs the plugin; `make plugin-update` after each change under `plugin/`.
- `make help` lists the rest.

## Rules

- Tests run against fakes: `httptest` handlers for `client`, a recording fake of `hookAPI` for the hooks, a small go-sdk server for the MCP bridge.
- Only `api` and `client` are importable by other modules. Their Go API may change in any v0 release; the JSON they send and read may not, see the next rule.
- `api` defines the wire: the `/v1` and MCP shapes, the enums, limits and error codes, the MCP tools and `Instructions`, and the rules both ends apply, such as `OneLine`. Define each once there and never copy it. The client must work with a server that does not know a new field yet, and ignore fields it does not know.
- The MCP tools in `api/tools.go` stay at twelve: adding one means removing one. `api/tools_test.go` checks the count.
- The `mcp` command speaks the protocol on stdout, and `hook` prints nothing there, except `hook session-start`, `hook prompt` and `hook tool-use`, which answer Claude Code with one JSON object each and only when there is context to add. Every command logs to stderr through `stderrLogger` in `cmd/bellek/app.go`.
- A hook exits zero whatever happens at runtime, so a missing server never blocks Claude Code. A malformed invocation still fails loudly, so a typo in `hooks.json` or an event a future Claude Code sends is not swallowed.
- Capture text, the session name and the location a hook or the MCP bridge sends go through `internal/redact` before they leave the machine. The server does not scan again. [docs/privacy.md](docs/privacy.md) lists what each hook sends; keep it true when a hook changes.
- `plugin/hooks/hooks.json` stays identical to `hooks/claude-code/settings.example.json`, which `cmd/bellek/plugin_test.go` checks.
- The client reads its settings from the environment only, never from a file.
- Every `run` step in `.github/workflows` is a single make target, so CI runs the same on a laptop. `cmd/bellek/workflows_test.go` checks it.
- Release asset names carry no version, so `releases/latest/download/<name>` always works. The tag, the GitHub release, the plugin manifest and the Homebrew formula use the same `vX.Y.Z`.
- JSON field names are camelCase.
- Write a comment only when the code cannot say it.
