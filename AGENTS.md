# AGENTS.md — asn-lookup

## What this is

A CLI + local MCP server for IP↔AS lookups from the IPinfo Lite database. It
downloads the free Lite CSV once, builds a compact local index, and answers
IP→AS and ASN→prefix queries fully offline. Purpose: let AI tools investigate
IPs without excessive ipinfo API calls.

## Build & test

```bash
make build      # → dist/asn-lookup  (NEVER `go build` directly)
make test       # go test -race -cover ./...
make check      # lint + test + build-all
make build-all  # cross-compile linux/{amd64,arm64}, darwin/arm64, windows/amd64
make verify-release  # gate: .notarized marker + freshness (run before upload)
```

Go 1.25+. **No external dependencies** — standard library only.

## Layout

```
main.go                 Entry point; sets main.version, calls app.Run.
internal/asndb/         Core: CSV parse, index build/serialize, Open, LookupIP/LookupASN.
  record.go             Record / IPResult / ASNResult types.
  csv.go                IPinfo Lite CSV parsing (columns resolved by header name).
  index.go              On-disk format, Builder, DB, binary-search lookups.
internal/config/        Sectioned-TOML subset parser + env/flag resolution.
internal/ipinfo/        Fetcher interface + HTTPFetcher; token redaction.
internal/engine/        Ties config+fetcher+index: LoadDB, Update (atomic), IsStale.
internal/app/           CLI: dispatch, ip/asn/update/doctor/mcp commands, output.
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 MCP server + tools.
  usage.md              Embedded get_usage manual (pinned by usage_test.go).
```

## Key design decisions

- **No MMDB.** The reverse lookup (ASN→prefixes) is impossible with an IP-keyed
  MMDB, so the CSV must be ingested regardless. Once ingested, forward lookup is
  served from the same data — MMDB adds nothing. Everything is one CSV-derived
  index, keeping the binary dependency-free (`net/netip` handles CIDR/v4v6).
- **Reverse view is derived, not stored.** `LookupASN` scans the forward entries
  rather than keeping a second index. It cannot drift from `LookupIP`, and saves
  memory. The scan is O(n) but fine at this scale for interactive use.
- **Index loaded into memory** (whole file read, then parsed into slices). The
  on-disk layout is fixed-width and mmap-ready, so a zero-copy open is a future
  optimization that would not change the file format.
- **update is explicit.** The CLI never auto-downloads; it prints a hint. The MCP
  server exposes `update_db` so an AI can refresh the database itself.
- **Engine is shared** by CLI and MCP so their behaviour cannot diverge.
- **Large `lookup_asn` is paged, and the server has no filesystem.** Some ASNs
  have hundreds of thousands of prefixes, so the tool returns a summary plus one
  page (`limit`/`offset`/`has_more`, `prefix_count` = the true total). It used to
  write the full list to an agent-provided `workspace_root`, which made the
  server depend on the client owning a filesystem it could name and put the "too
  big for the model" judgement in the one process that cannot know the model's
  context window. Do not reintroduce file mediation: a page too large to hold is
  a `limit` that was too large.

## Gotchas

- **Flag order:** `ip`/`asn` accept flags interspersed with positional args via
  `parseInterspersed` (Go's `flag` otherwise stops at the first non-flag). Keep
  using it if you add positional commands.
- **Token is a secret:** never log the raw download URL — use `ipinfo.Redact`.
  Query param carries the token as the endpoint requires; it is redacted in
  errors.
- **Build determinism:** `asndb.Builder.Serialize` takes `generatedUnix` as an
  argument; it must not read the clock. Only `engine.Update` stamps `time.Now`.
- **CSV columns by name:** the parser maps columns by header, tolerating
  reordering; only `network` is mandatory.
- **Attribution:** IPinfo Lite is CC BY-SA 4.0. Keep the credit in `version`,
  `--help`, and the READMEs. Do not add DB redistribution.
- **Tool schemas are closed and the decoder enforces it.** Every `inputSchema`
  is built by `obj()` in `internal/mcp/tools.go`, which sets
  `additionalProperties: false` (org ADR-021 §10), and
  `TestEveryToolSchemaIsValidAndClosed` fails if a tool escapes it — so build a
  new schema with `obj()`, not a map literal. The flag is only the *declared*
  half; the enforcing half is `decodeArgs()` beside it
  (`json.Decoder.DisallowUnknownFields`), which every tool handler decodes
  through, including the argument-less ones. **A call carrying an argument this
  server does not declare now fails, naming the field, instead of being
  silently ignored** — deliberate, per ADR-021 §4, because the schema says what
  is allowed and the decoder is what actually refuses. `decodeArgs` also stops
  discarding the decode error, so a wrong-typed argument no longer runs the tool
  on zero values. Omitted or `null` arguments still mean the empty object. Do
  not add a `_ = json.Unmarshal` back, and do not add a compatibility shim: the
  ADR's one-release grace covers only the retired `workspace_root` spellings.
- **get_usage manual:** `internal/mcp/usage.md` is embedded and returned by the
  `get_usage` tool; the initialize `instructions` field points clients to it.
  When you add/rename a tool or a result field, update usage.md — `usage_test.go`
  fails if the manual omits a tool name or a documented key term.

## Data source

IPinfo Lite: `https://ipinfo.io/data/ipinfo_lite.csv.gz?token=$TOKEN`
(free token). ~3.5M rows, daily updates, CC BY-SA 4.0.
Columns: `network, country, country_code, continent, continent_code, asn,
as_name, as_domain`.
