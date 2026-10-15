# Contributing

Bug reports and pull requests are welcome.

## Before you start

- Open an issue first for anything larger than a small fix, so we can agree on the change before you write it.
- The client talks to the Bellek server through the `/v1` API and `/mcp`. A change that needs a new field or route on the server starts as an issue.

## Working on the code

You need Go, the version in `go.mod`, and git.

```bash
make test    # the tests, with the race detector
make lint    # gofmt, then golangci-lint when it is installed, else go vet
make ci      # both, as CI runs them
make build   # dist/bellek for this machine
```

Read [CLAUDE.md](CLAUDE.md) for the rules the code follows. The ones people trip on:

- `bellek mcp` speaks the protocol on stdout and a hook prints at most one JSON answer there. Logs go to stderr.
- A hook exits zero whatever happens at runtime, so a missing server never blocks Claude Code.
- Text a hook sends, and the location the MCP bridge adds, goes through `internal/redact` first.

## Pull requests

- One change per pull request, with tests.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org), such as `fix(hook): keep the spool when the server is down`.
- CI must be green: it runs `make lint` and `make test`.
