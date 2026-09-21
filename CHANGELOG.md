# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **Every MCP tool input schema is closed.** The schemas omitted
  `additionalProperties: false`, so a mistyped argument read as a legitimate one
  to any client that validates against them. Schemas are now built through a
  single `obj()` helper that sets the flag, and an arch test fails if a tool's
  schema omits it — org ADR-021 §10 requires the test as well as the flag,
  because a rule stated only in prose is re-decided by whoever adds the next
  tool. The server's own argument decoding is unchanged and still lenient: it
  does not use `DisallowUnknownFields`, so an unknown argument that reaches it
  is ignored rather than refused.

## [0.2.0] - 2026-08-31

### Changed

- **`lookup_asn` pages its prefixes inline.** The MCP tool no longer writes the
  full list to a file: it returns one page in `prefixes`, bounded by `limit`
  (default 50) and walked with the new `offset`, with `has_more` saying whether
  any are left. `prefix_count` is still the true total, so nothing is dropped.

  Migration: replace a `workspace_root` call plus a file read with a loop that
  advances `offset` by `limit` while `has_more` is true. `limit: 0` still means
  "all of them", for an AS you already know is small.

### Added

- `lookup_asn` takes `offset`, and every result carries `offset`, `limit` and
  `has_more`.

### Removed

- `lookup_asn`'s `workspace_root`, `workspace_id` and `format` arguments, and
  the `prefixes_file` / `truncated` / `preview` / `note` result fields. `format`
  only ever chose the encoding of the written file.
- The `[mcp] workspace` config key and `ASN_LOOKUP_WORKSPACE`. The server has no
  output directory: it touches no filesystem, so it works unchanged against a
  client that has none.

## [0.1.0] - 2026-07-13

### Added

- Initial implementation.
- `ip` — IP → AS (ASN, name, domain) + country/continent lookup; multiple
  addresses and stdin input; table and JSON Lines output.
- `asn` — ASN → announced IP prefixes (IPv4 + IPv6); accepts `AS15169` or
  `15169`; table and JSON Lines output.
- `update` — download the IPinfo Lite database (`ipinfo_lite.csv.gz`) and
  rebuild the local index atomically.
- `doctor` — report database presence, freshness, and configuration.
- `mcp` — local stdio MCP server exposing `get_usage`, `lookup_ip`,
  `lookup_asn`, `update_db`, and `db_status`. `get_usage` returns an embedded
  operating manual (tools, workspace model, recovery table), and the server
  advertises it via the initialize `instructions` field.
- File-mediated large `lookup_asn` results: the summary + a preview are returned
  inline, and the full prefix list is written to a file in an agent-prepared
  `workspace_root` (with `os.Root` symlink containment) so a huge ASN cannot
  flood the caller's context. `format` (`cidr`/`json`) is chosen per request.
- `asn --count` (counts only) and `asn -n/--limit N` (cap printed prefixes).
- `[mcp] workspace` config / `ASN_LOOKUP_WORKSPACE` env for the default output
  directory.
- Compact, self-contained on-disk index (no MMDB): deduplicated AS/geo records
  plus family-split sorted address ranges; standard library only.
- Configuration via sectioned TOML (`~/.config/asn-lookup/config.toml`) and
  `IPINFO_TOKEN` / `ASN_LOOKUP_TOKEN` environment variables; token redacted in
  logs.
- Freshness warning for databases older than 30 days.
- IPinfo Lite attribution (CC BY-SA 4.0) in `version`, `--help`, and the README.
