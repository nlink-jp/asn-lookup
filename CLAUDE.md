# CLAUDE.md — asn-lookup

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

Local IP↔AS lookup CLI + MCP server backed by the IPinfo Lite database. Reads
IP/ASN input, answers from a compact local index (built from the Lite CSV), and
serves the same engine over stdio MCP.

## Build & test

```bash
make build       # Build → dist/asn-lookup  (never `go build` directly)
make test        # Tests with race detector + coverage
go test ./...    # Same without Makefile
```

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/asndb/         Parse CSV, build/open index, LookupIP/LookupASN (pure)
internal/config/        Sectioned-TOML subset + env/flag resolution
internal/ipinfo/        Fetcher interface + HTTPFetcher (token redaction)
internal/engine/        LoadDB / Update (atomic) / IsStale — shared by CLI & MCP
internal/app/           Dispatch + ip/asn/update/doctor/mcp + output formatting
internal/mcp/           MCP tools on nlink-jp/mcptool (conventions; ADR-0002) over nlink-jp/mcp (both eras; ADR-0001)
```

Core logic takes io.Reader/io.Writer and injected dependencies for testability.
**No third-party dependencies** — the standard library, plus nlink-jp modules
written on the same rule (`github.com/nlink-jp/mcp` and `github.com/nlink-jp/mcptool`,
organization ADR-030 and ADR-031). See
[docs/en/architecture.md](docs/en/architecture.md) for the "why".

## Key conventions

- No MMDB: one CSV-derived index serves both directions; reverse lookup is a scan
  of the forward entries (cannot drift, less memory).
- `ip`/`asn` accept flags interspersed with positionals (`parseInterspersed`).
- Token is secret: never log the raw URL — use `ipinfo.Redact`.
- Build is deterministic: `Serialize` takes `generatedUnix`; only `engine.Update`
  reads the clock.
- Data is IPinfo Lite (CC BY-SA 4.0): keep attribution in `version`, `--help`,
  READMEs; never redistribute the DB.

## Communication Language

All communication between contributors and Claude Code is conducted in **Japanese**.
