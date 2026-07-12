# RFP: asn-lookup

> Generated: 2026-07-12
> Status: Draft

## 1. Problem Statement

In security investigation and operations, it is common to need the owning AS
(ASN, organization name, domain) and country/continent of an IP address, or
conversely the list of IP prefixes held by a given ASN. Hitting the ipinfo API
for every lookup raises problems around rate limits, cost, and offline
availability — and when an AI tool (e.g. Claude Code) queries frequently over
MCP, the number of API calls becomes excessive.

`asn-lookup` solves this by **ingesting the free IPinfo Lite download database
locally and providing bidirectional IP↔AS lookups plus country/continent info,
fully offline, as both a CLI tool and a local MCP server**. The target users are
the org operator and their AI tools (MCP clients). This enables IP investigation
from AI tools while eliminating excessive API calls.

## 2. Functional Specification

### Commands / API Surface

Single binary + subcommands (util-series standard; `mcp` subcommand turns it
into an MCP server).

| Command | Purpose |
|---|---|
| `asn-lookup ip <IP>...` | IP → `{asn, as_name, as_domain, country, country_code, continent, continent_code}`. Multiple IPs allowed; reads from stdin when no args given |
| `asn-lookup asn <ASN>...` | ASN → `{as_name, as_domain, prefixes[]}` (IPv4/IPv6 prefix list). Accepts `AS15169` / `15169`. No representative country is attached |
| `asn-lookup update` | Download IPinfo Lite (`ipinfo_lite.csv.gz`) with a token → rebuild local index |
| `asn-lookup doctor` | Diagnose DB presence, freshness (build date), token config, index integrity |
| `asn-lookup mcp` | Run as a local MCP server (stdio) |
| `asn-lookup version` | Version (`git describe`) |

**MCP tools** (`asn-lookup mcp`):

| Tool | Purpose |
|---|---|
| `lookup_ip` | IP → AS info + country/continent |
| `lookup_asn` | ASN → prefix list |
| `update_db` | Lets the AI itself trigger DB download/rebuild |
| `db_status` | Check DB freshness and integrity |

### Input / Output

- Default output is a human-readable aligned table. `-j` / `--json` emits
  **JSONL** (one record per line, for pipes / machine processing). MCP always
  returns structured JSON.
- stdin support (`cat ips.txt | asn-lookup ip`) to honor the util-series pipe
  philosophy.
- **When the DB is not present**: the CLI does not auto-download; it exits with
  guidance to "run `asn-lookup update`". Over MCP, the AI can run the update
  itself via the `update_db` tool.
- **Freshness warning**: when the DB is stale, `doctor` and queries show a
  "DB is X days old" warning (no auto-update).

### Configuration

Secrets are never committed. Sectioned TOML.

- Token: `IPINFO_TOKEN` env var > `[ipinfo] token = "..."` in
  `~/.config/asn-lookup/config.toml`
- DB / index location: defaults to `~/.local/share/asn-lookup/` (override with
  `--db`)

### External Dependencies

- The only runtime external service is the ipinfo download endpoint
  (`https://ipinfo.io/data/ipinfo_lite.csv.gz?token=$TOKEN`). Queries are offline.
- Go standard library only: `net/netip` (CIDR / IPv4v6 parsing), `compress/gzip`,
  `encoding/csv`, `encoding/json`. **No MMDB; zero external dependencies.**
- Index: at `update` time, the CSV (~3.57M rows) is parsed into a sorted range
  array for forward lookup plus an `asn→prefixes` reverse index, persisted as a
  **compact on-disk index**. Queries mmap it for fast search (avoiding load cost
  on one-shot CLI runs; the same mechanism serves the long-lived MCP process).

## 3. Design Decisions

- **Go**: util-series standard, single-binary distribution. `net/netip` handles
  CIDR / IPv4v6 with the standard library alone. macOS signing + notarization
  flow already established.
- **Zero external dependencies**: same stance as nlk / mcp-guardian. Avoid MMDB
  (`oschwald/maxminddb-golang`). The reverse lookup (AS→IP) **cannot be done with
  an IP-keyed MMDB** and requires CSV ingestion anyway; once the CSV is ingested,
  forward lookup is covered by the same data, so MMDB offers no benefit. Hence a
  single CSV-derived index.
- **Complements existing tools**: an IOC enrichment foundation for
  cybersecurity-series (ai-ir2 / ioc-collector / cti-graph / mail-analyzer) — e.g.
  attaching AS/country to IPs collected by ioc-collector. Usable as a general
  utility from AI tools (MCP).
- **Out of scope**:
  - Detailed geolocation (city, lat/long) not present in IPinfo Lite
  - Real-time paid DB / API lookups (local DB only)
  - whois / RDAP / BGP routing measurement, reverse DNS / PTR
  - **DB redistribution** (each user downloads with their own token — avoids
    CC BY-SA share-alike obligations and stays compliant)

## 4. Development Plan

### Phase 1: Core

- `net/netip`-based CIDR parser, gzip CSV ingestion, forward index (sorted ranges
  + binary search), reverse index (`asn→prefixes`), on-disk index build/mmap load.
- `ip` / `asn` / `update` / `doctor` subcommands.
- Pure-function-centric with injectable dependencies (download behind an
  interface, mockable in tests). Tested against a small real-data sample.
- **Independently reviewable.**

### Phase 2: Features

- `mcp` subcommand (stdio MCP: `lookup_ip` / `lookup_asn` / `update_db` /
  `db_status`). Port the data-toolbox-mcp skeleton.
- Finalize JSONL output / stdin support, freshness warning, `--db`.
- **Independently reviewable.**

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md.
- **ipinfo attribution (CC BY-SA 4.0)** in output and README.
- Makefile (`make build` / `make build-all`), signing + notarization, GitHub
  release, umbrella submodule pointer update, org profile addition, `check-org.sh`.

## 5. Required API Scopes / Permissions

- Only a **single free ipinfo download token**.
- No OAuth, no privileged IAM roles. The token is used solely to download the DB.

## 6. Series Placement

Series: util-series
Reason: A pipe-friendly lookup / transformation CLI that is self-contained. While
it has cybersecurity-leaning uses (IOC enrichment), it fits best as a general
network-information utility in util-series. Confirmed by the user.

## 7. External Platform Constraints

- IPinfo Lite is under **Creative Commons Attribution-ShareAlike 4.0
  (CC BY-SA 4.0)** → ipinfo attribution is mandatory in output and README, and the
  DB itself is not redistributed.
- Daily updates; scale ~3.57M rows (IPv4 + IPv6, as of 2026-07-10).
- The download endpoint's rate / bandwidth limits are unpublished → `update` is
  expected roughly daily; frequent downloads are unnecessary. Queries are offline
  and thus unaffected by runtime rate limits.
- A free token is required for downloads.

---

## Discussion Log

- **Idea**: conceived as a local tool that both avoids excessive ipinfo API calls
  and enables offline IP investigation from AI tools (MCP).
- **Data-source research**: confirmed via ipinfo's official docs that the free
  **IPinfo Lite** download DB (MMDB / CSV / JSON / Parquet, daily updates,
  CC BY-SA 4.0, fields: `network(CIDR)` / `country` / `country_code` /
  `continent` / `continent_code` / `asn` / `as_name` / `as_domain`) matches the
  requirements.
- **Decision to drop MMDB**: requirement #2 (ASN→IP prefix reverse lookup) is
  impossible with an IP-keyed MMDB. Reverse lookup requires CSV ingestion, and
  once the CSV is ingested forward lookup is served from the same data — so MMDB
  is not used and everything is a single CSV-derived index. This also aligns with
  the util-series "zero external dependencies" stance (`net/netip` handles
  CIDR/v4v6 with the standard library alone).
- **First-version scope**: in addition to bidirectional IP↔AS, include the
  country/continent info that ships free in the same DB (near-zero added cost).
- **`update` behavior**: the CLI does not auto-download (avoids unexpected network
  traffic) and only shows guidance; the MCP server exposes an `update_db` tool so
  the AI can refresh the DB itself.
- **Freshness**: a stale DB triggers a warning at query time and in `doctor` (no
  auto-update).
- **Country info in ASN reverse lookup**: an ASN spans multiple countries and a
  single representative country would be misleading, so none is attached.
- **Tool name**: `-lens`-family names (claude-usage-lens / active-lens) were
  considered, but `asn-lookup` was chosen for its clear, self-explanatory intent.
- **Series**: confirmed as util-series.
