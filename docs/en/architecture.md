# asn-lookup architecture

This document explains *why* the tool is built the way it is. For how to use it,
see the [README](../../README.md); for an operational summary, see
[AGENTS.md](../../AGENTS.md).

## Goal

Answer two questions locally and offline, fast enough for interactive and
AI-driven use, without repeatedly calling the ipinfo API:

1. **IP → AS**: which ASN (and country/continent) owns an address?
2. **AS → IP**: which prefixes does an ASN announce?

## Why IPinfo Lite, and why no MMDB

IPinfo publishes the free **IPinfo Lite** database in MMDB, CSV, JSON, and
Parquet. MMDB is optimized for one direction only: address → data. Our second
requirement — ASN → prefixes — is a *reverse* query that an IP-keyed MMDB cannot
serve without a full scan of its tree, and MMDB gives us no easy way to enumerate
prefixes per ASN.

Since the reverse query forces us to ingest the raw CSV anyway, the MMDB buys
nothing: once the CSV is parsed, the forward query is served from the same data.
So the tool ingests the CSV and builds **one** index that serves both
directions. A welcome side effect: parsing CSV with `net/netip` needs no
third-party library, so the binary has **zero external dependencies** — matching
the house style of `nlk` and `mcp-guardian`.

## The index

`update` streams the gzipped Lite CSV and builds a compact, self-contained index
file:

- **Records** — the AS/geo attributes (`asn, as_name, as_domain, country,
  country_code, continent, continent_code`) are deduplicated. Millions of
  networks collapse onto a much smaller set of distinct records, referenced by
  index. Strings are interned into a single table.
- **Forward entries** — every network becomes a `(start, prefix-bits, record)`
  entry, split into an IPv4 table (`uint32` start) and an IPv6 table (`[16]byte`
  start), each sorted ascending by start address.

The on-disk layout is a small header, the string table, the record table, then
the v4 and v6 entry arrays — all fixed-width. `Open` reads the whole file into
memory once and slices it into typed arrays.

### Forward lookup (IP → AS)

The Lite database is a partition: networks do not overlap. So for an address we
binary-search the family's sorted entries for the greatest `start ≤ addr`, then
confirm the candidate prefix actually `Contains` the address. If it does not, the
address falls in an unallocated gap → not found. One `O(log n)` search, no
allocation.

### Reverse lookup (AS → IP)

Rather than store a second ASN-keyed index, `LookupASN` **scans** the forward
entries and collects those whose record has the target ASN, reconstructing each
prefix from `(start, bits)`. Two consequences:

- **No drift.** The reverse answer is derived from the exact same entries the
  forward path uses, so the two can never disagree.
- **Less memory.** No duplicate structure to build or hold.

The scan is `O(n)`, but at a few million entries it completes in milliseconds —
acceptable for the occasional reverse lookup an interactive or MCP session makes.
If reverse throughput ever matters, a sorted ASN index can be added to the file
format without touching the forward path.

## Layering

```
asndb   ── pure index: parse, build, open, lookup (no I/O beyond io.Reader/Writer)
config  ── settings resolution (TOML subset + env + flags)
ipinfo  ── Fetcher interface (+ HTTP impl); mockable downloads
engine  ── LoadDB / Update (atomic) / IsStale — the shared use-cases
app     ── CLI shell (dispatch, commands, output formatting)
mcp     ── stdio JSON-RPC 2.0 server exposing the same engine as tools
```

The **engine** is the single place the CLI and the MCP server meet, so a change
to update or freshness logic applies to both. Downloads sit behind a `Fetcher`
interface so `engine` and the commands are tested without network access.
Serialization takes the source timestamp as a parameter, keeping index builds
deterministic and testable.

## File-mediated large results

Reverse lookups are unbounded: an ASN can map to hundreds of thousands of Lite
network rows (Cloudflare has ~590k, dominated by geo-partitioned IPv6). Returning
that inline through an MCP tool would flood the model's context (tens of MB of
JSON). Returning a naive truncation would silently hide most of the answer.

So `lookup_asn` hands the list over a page at a time: a compact summary
(`prefix_count`, `v4_count`, `v6_count`) plus one page of prefixes, bounded by
`limit` and walked with `offset`, with `has_more` saying whether any are left.
`prefix_count` is always the true total, so nothing is hidden silently.

It used to write the full list to a caller-supplied `workspace_root` and return
the path. That was the wrong side of the wire twice over. It made the server
depend on the client owning a filesystem it could name — a client speaking MCP
over a transport with no shared disk could not read its own result. And it put
the "too big for the model" judgement in the one process that cannot know the
model's context window; only the client knows that number. The server now writes
nothing, owns no output directory, and takes no path argument.

`lookup_ip` stays inline: its output is proportional to the caller-supplied input
list, so it cannot explode.

## Security & privacy

- The ipinfo token is a secret. It is sent as the endpoint's documented query
  parameter (and an `Authorization: Bearer` header), but any URL that appears in
  an error or log is passed through `ipinfo.Redact` first.
- Lookups are offline; nothing about the queried addresses leaves the machine.

## Licensing

The IPinfo Lite data is **CC BY-SA 4.0**: attribution to IPinfo is required and
surfaced in `version`, `--help`, and the READMEs. The tool downloads the data
with the user's own token and never redistributes the database, so the
share-alike obligation is not triggered for this MIT-licensed code.
