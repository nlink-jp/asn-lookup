# asn-lookup MCP — operating manual

This server answers IP↔AS questions from a local copy of the **IPinfo Lite**
database (CC BY-SA 4.0; attribution to IPinfo required). All lookups are offline;
only `update_db` touches the network.

Call `db_status` first to confirm a database exists and is fresh. If it does not
exist, call `update_db` (a token must be configured, otherwise ask the user to
set `IPINFO_TOKEN`).

## Tools

### `get_usage`
Returns this manual. No arguments.

### `db_status`
Reports `generated`, record/prefix counts, `stale`, `age_days`, and the database
`path`. No arguments. Returns an error result when no database exists yet.

### `update_db`
Downloads the latest IPinfo Lite database and rebuilds the local index. No
arguments. Requires a configured ipinfo token. Returns counts on success; an
error result (with the reason) on failure — a missing token means the user must
set `IPINFO_TOKEN` or `[ipinfo] token` in the config file.

### `lookup_ip`
IP → AS + country/continent.
- Arguments: `ip` (string) **or** `ips` (array of strings). At least one required.
- Result: a JSON array, one object per input, each with `input`, `found`, and —
  when found — `ip`, `network`, `asn`, `as_name`, `as_domain`, `country`,
  `country_code`, `continent`, `continent_code`. Invalid addresses come back as
  `found:false` with `error:"invalid address"`. Private/unmapped addresses are
  `found:false`.

### `lookup_asn`
ASN → the IP prefixes it announces in IPinfo Lite.
- Arguments:
  - `asn` (string, e.g. `"AS15169"` or `"15169"`) **or** `asns` (array).
  - `limit` (integer, default 50): prefixes per page. `0` means all of them —
    only safe for an AS you already know is small.
  - `offset` (integer, default 0): 0-based index of the first prefix to return.
- Result: a JSON array, one object per input, each with `input`, `found`, `asn`,
  `as_name`, `as_domain`, `prefix_count`, `v4_count`, `v6_count`, `offset`,
  `limit`, `has_more`, and `prefixes` — this page, always inline.

## Arguments are strict

Every tool refuses an argument it does not declare, naming it:
`arguments: json: unknown field "offest"`. A wrong-typed argument is refused the
same way. Nothing runs before the arguments decode, so a rejected call reads no
database and downloads nothing — fix the name or the type and call again. This
is the enforcing half of the closed schemas (org ADR-021 §4); a misspelt
`offset` used to be dropped, which made every page of a large AS come back as
the first one.

## Paging

Some ASNs map to hundreds of thousands of prefixes (Cloudflare ≈ 590k), so
`lookup_asn` hands them over a page at a time. No file is written and no path
comes back.

Walk a large AS by adding `limit` to `offset` while `has_more` is true.
`prefix_count` is the true total, so you always know how far you have to go, and
nothing is silently dropped.

## Recovery table

| Symptom (result text) | What it means | What to do |
|---|---|---|
| `arguments: json: unknown field "…"` | An argument name this tool does not declare — usually a typo | Fix the spelling and call again; the named field is the offending one |
| `arguments: json: cannot unmarshal …` | An argument of the wrong JSON type (an ASN must be a string, `limit`/`offset` integers) | Check the argument's type in the tool list above and call again |
| `no local database …` | The index has not been built | Call `update_db` (needs a token) |
| `no ipinfo token configured …` | `update_db` has no token | Ask the user to set `IPINFO_TOKEN` or `[ipinfo] token` |
| `lookup_asn` → `has_more:true` | More prefixes exist beyond this page | Re-request with `offset` advanced by `limit`; `prefix_count` is the total |
| `lookup_asn` → the response is larger than you can hold | `limit` was too large for your context (or `0`) | Re-request with a smaller `limit` and page through |
| `found:false`, `error:"invalid address"` | The input was not a valid IP | Fix the input |
| `found:false` (no error) | Address/ASN not present in Lite (e.g. private IP) | Expected; no action |
| `db_status` → `stale:true` | Database older than 30 days | Call `update_db` to refresh |

## Attribution

Data: IPinfo Lite (https://ipinfo.io/lite), CC BY-SA 4.0. Credit IPinfo when you
present results derived from this database.
