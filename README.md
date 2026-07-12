# asn-lookup

Local IP↔AS lookups from the [IPinfo Lite](https://ipinfo.io/lite) database, as
a CLI **and** a local MCP server. Downloads the free IPinfo Lite database once,
builds a compact local index, and answers lookups fully offline — so AI tools
can investigate IP addresses without hammering the ipinfo API.

- **IP → AS**: given an IP, return its ASN, AS name, AS domain, and
  country/continent.
- **AS → IP**: given an ASN, return every IP prefix it announces.
- **CLI + MCP**: the same engine drives an interactive CLI and a stdio MCP server
  (`lookup_ip`, `lookup_asn`, `update_db`, `db_status`).
- **Zero external dependencies**: standard library only (`net/netip` + a
  self-contained on-disk index). No MMDB reader, no config libraries.

> **Data:** IPinfo Lite (<https://ipinfo.io/lite>), licensed
> [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/). Attribution to
> IPinfo is **required**. This tool downloads the database with your own token
> and does not redistribute it.

## Install

```bash
make build          # → dist/asn-lookup (signed on macOS)
```

Requires Go 1.25+. Cross-compile all platforms with `make build-all`.

## Quick start

1. Get a **free** token at <https://ipinfo.io/signup> (no card required).
2. Download the database and build the local index:

   ```bash
   export IPINFO_TOKEN=your_token_here
   asn-lookup update
   ```

3. Look things up:

   ```bash
   asn-lookup ip 8.8.8.8
   # IP       ASN      AS NAME     COUNTRY  NETWORK
   # 8.8.8.8  AS15169  Google LLC  US       8.8.8.0/24

   asn-lookup asn AS15169
   # AS15169  Google LLC  google.com  (N prefixes)
   #   8.8.4.0/24
   #   8.8.8.0/24
   #   ...
   ```

## Commands

| Command | Description |
|---------|-------------|
| `asn-lookup ip <IP>...` | Look up AS + country/continent per IP. Reads stdin when no args are given. |
| `asn-lookup asn <ASN>...` | List the prefixes announced by each ASN (`AS15169` or `15169`). Reads stdin when no args. |
| `asn-lookup update` | Download IPinfo Lite and rebuild the local index (atomic replace). |
| `asn-lookup doctor` | Report database presence, freshness, and configuration. |
| `asn-lookup mcp` | Run as a local MCP server over stdio. |
| `asn-lookup version` | Print the version and data attribution. |

### Output formats

- Default: an aligned, human-readable table.
- `-j` / `--json`: **JSON Lines** (one JSON object per input) for pipelines.

```bash
cat ips.txt | asn-lookup ip --json | jq 'select(.found) | .asn'
```

Unknown, unmapped, and invalid inputs are reported per line (they never abort
the batch). For `asn`, `--count` prints only the header/counts and `-n/--limit N`
caps the printed prefixes.

## MCP server

Register the server with an MCP client. For Claude Code:

```bash
claude mcp add asn-lookup -- /path/to/asn-lookup mcp
```

Or in a client config:

```json
{
  "mcpServers": {
    "asn-lookup": { "command": "/path/to/asn-lookup", "args": ["mcp"] }
  }
}
```

Tools (call `get_usage` first for the full manual and recovery table; the server
also advertises this via the MCP `instructions` field):

| Tool | Arguments | Purpose |
|------|-----------|---------|
| `get_usage` | — | Operating manual: tools, workspace model, recovery table |
| `lookup_ip` | `ip` (string) or `ips` (array) | IP → AS + country/continent |
| `lookup_asn` | `asn`/`asns`, `limit`, `format`, `workspace_root`, `workspace_id` | ASN → prefix list (large results file-mediated) |
| `update_db` | — | Download + rebuild the database (needs a token) |
| `db_status` | — | Generation date, record counts, staleness |

**Large ASN results are file-mediated.** Some ASNs map to hundreds of thousands
of prefixes (e.g. Cloudflare has ~590k in IPinfo Lite). `lookup_asn` always
returns a compact summary (`prefix_count`, `v4_count`, `v6_count`) plus an inline
preview; when the full list exceeds `limit` (default 50) it is written to a file
and only its path is returned (`prefixes_file`, `truncated: true`) — so a huge
ASN never floods the model's context. Following the pattern used by
voice-studio-mcp, the output directory is agent-provided: create a directory with
your own file tools and pass it as `workspace_root` (essential in sandboxed
environments); omit it to use the server default. All writes are confined to the
workspace with `os.Root` (planted symlinks cannot escape). Choose the file
`format` (`cidr` or `json`) per request.

The database is not downloaded automatically: the CLI prints a hint to run
`update`, while an MCP client can call `update_db` itself. A stale database
(older than 30 days) produces a warning but is never auto-refreshed.

## Configuration

Settings resolve in this order (later wins): config file → environment → flags.

**Token** (required only for `update`):

- `IPINFO_TOKEN` (or `ASN_LOOKUP_TOKEN`) environment variable, or
- `[ipinfo] token` in the config file.

**Config file** — `~/.config/asn-lookup/config.toml`
(honors `XDG_CONFIG_HOME`). See [`config.example.toml`](config.example.toml):

```toml
[ipinfo]
token = "your_token_here"
# lite_url = "https://ipinfo.io/data/ipinfo_lite.csv.gz"

[db]
# path = "~/.local/share/asn-lookup/asndb.bin"

[mcp]
# Default output directory for file-mediated results (ASN_LOOKUP_WORKSPACE).
# Callers may override per request with workspace_root.
# workspace = "~/.local/state/asn-lookup/workspace"
```

**Index location** — `~/.local/share/asn-lookup/asndb.bin`
(honors `XDG_DATA_HOME`; override with `--db`).

The token is never written to logs: any URL surfaced in errors has the token
redacted.

## How it works

`update` streams the gzipped IPinfo Lite CSV, parses each `network` row with
`net/netip`, and writes a compact index: deduplicated AS/geo records plus
family-split, sorted address ranges. `ip` lookups binary-search the ranges;
`asn` lookups scan them (the reverse view is derived from the same data, so it
can never drift from the forward view). See
[docs/en/architecture.md](docs/en/architecture.md).

## Development

```bash
make test     # go test -race -cover ./...
make build    # → dist/asn-lookup
make check    # lint + test + build-all
```

## License

Code: [MIT](LICENSE). Database: IPinfo Lite, CC BY-SA 4.0 — attribution to
IPinfo required; not redistributed by this tool.
