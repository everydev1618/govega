# Contributing to Vega

Thanks for your interest. Vega is MIT-licensed and contributions are welcome.

## Getting set up

```bash
git clone https://github.com/everydev1618/govega
cd govega
go build ./...     # compile everything
go test ./...      # unit tests + fast e2e
```

The frontend (React 19 + Vite + Tailwind) lives in `serve/frontend` and is
embedded into the binary via `//go:embed`. `serve/frontend/dist` is committed,
so you only need Node if you are changing the UI:

```bash
make frontend-build   # rebuild dist/ — commit the result
make build            # frontend + Go binary → bin/vega
```

## Tests

Vega has three tiers. **Every change gets a unit test**, written first: add the
failing test, confirm it fails, implement, confirm it passes.

| Tier | Where | How to run |
|------|-------|-----------|
| Unit / integration | colocated `*_test.go` | `go test ./...` |
| Deterministic full-stack e2e | `serve/e2e_test.go` | `go test ./serve` (`-short` skips the ~45s restart scenarios) |
| Live API smoke | `live_smoke_test.go` | `VEGA_E2E_LIVE=1 go test -run TestLive .` |

The live tier calls the real Anthropic API, needs `ANTHROPIC_API_KEY`, and costs
a few cents. It is not part of CI. Run it before releases and after any change
to `llm/` request building, the model tables, or content-block handling.

Store-layer tests that must pass on both backends use `forEachStore`
(`serve/store_dual_test.go`). SQLite always runs; the Postgres subtest runs only
when `VEGA_TEST_POSTGRES_URL` is set.

## Before you open a PR

```bash
gofmt -l .     # must print nothing
go vet ./...
go test ./...
```

CI runs exactly these. Keep commits focused and write a message that explains
*why*, not just what.

## Running the app

See `docs/PLAYGROUND.md` for a verified walkthrough. Always pass an isolated
`--db`; the default touches your real `~/.vega` state:

```bash
./bin/vega serve --addr 127.0.0.1:3001 --db /tmp/scratch.db
```

## Architecture

`CLAUDE.md` in the repo root is the orientation document — agent/process model,
package map, persistence, error classification. Start there.

## Reporting bugs

Open an issue with the Vega version (`vega version`), your OS, and the smallest
reproduction you can manage. For anything security-related, read `SECURITY.md`
first — please do not open a public issue.
