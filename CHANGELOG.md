# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
