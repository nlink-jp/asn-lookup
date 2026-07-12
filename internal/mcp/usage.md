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
  - `limit` (integer, default 50): max prefixes inlined before a file is written.
  - `format` (`"cidr"` or `"json"`, default `cidr`): format of the written file.
  - `workspace_root` (string): **absolute path** to a directory you prepared with
    your own file tools; the full prefix file is written here. Omit to use the
    server default (may be unwritable in a sandbox — prefer passing this).
  - `workspace_id` (string): optional single-segment subdirectory under the root.
- Result: a JSON array, one object per input, each with `input`, `found`, `asn`,
  `as_name`, `as_domain`, `prefix_count`, `v4_count`, `v6_count`, and:
  - small result → `prefixes` (the full inline list), `truncated:false`.
  - large result → `preview` (first `limit`), `truncated:true`, and
    `prefixes_file` (absolute path to the full list). **Read that file** for the
    complete set; do not expect the full list inline.

## Workspace model (why files, not bytes)

Some ASNs map to hundreds of thousands of prefixes (Cloudflare ≈ 590k). Returning
those inline would flood your context, so large `lookup_asn` results are written
to a file and only the path is returned. `prefix_count` always reflects the full
total — nothing is silently dropped.

The output directory is **caller-provided**: create a writable directory with
your own file tools and pass it as `workspace_root`. This is required in
sandboxed environments where the server cannot write under `$HOME`. Writes are
confined to the workspace (kernel-enforced via `os.Root`); the filename is
server-generated (`AS<n>-prefixes.<format>`), so you never control the leaf name.

## Recovery table

| Symptom (result text) | What it means | What to do |
|---|---|---|
| `no local database …` | The index has not been built | Call `update_db` (needs a token) |
| `no ipinfo token configured …` | `update_db` has no token | Ask the user to set `IPINFO_TOKEN` or `[ipinfo] token` |
| `lookup_asn` → `truncated:true`, `prefixes_file` set | Full list written to a file | Read `prefixes_file` for all prefixes |
| `lookup_asn` → `note` mentions `workspace_root` | The output file could not be written | Create a writable directory and pass its absolute path as `workspace_root` |
| `found:false`, `error:"invalid address"` | The input was not a valid IP | Fix the input |
| `found:false` (no error) | Address/ASN not present in Lite (e.g. private IP) | Expected; no action |
| `db_status` → `stale:true` | Database older than 30 days | Call `update_db` to refresh |

## Attribution

Data: IPinfo Lite (https://ipinfo.io/lite), CC BY-SA 4.0. Credit IPinfo when you
present results derived from this database.
