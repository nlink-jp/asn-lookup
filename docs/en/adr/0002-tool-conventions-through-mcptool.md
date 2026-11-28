# ADR-0002: The tool conventions come from nlink-jp/mcptool

| Field | Value |
|-------|-----|
| Status | **Accepted** (2026-11-28) |
| Date | 2026-11-28 |
| Binds | asn-lookup |
| Decision makers | nlink-jp maintainers |
| Triggered by | Organization ADR-031 (the MCP tool conventions live in one module; the servers stop copying them). Issue nlink-jp/asn-lookup#2 |

The Japanese version (`docs/ja/adr/0002-tool-conventions-through-mcptool.ja.md`) is the source.

## Context

ADR-0001 moved the protocol layer onto nlink-jp/mcp. The conventions on top of it stayed copied here:
- `internal/mcp/server.go`'s shell (`toolResult`, `textResult`, `jsonResult`, the dispatch);
- `decodeArgs` and `jsonNames`;
- the startup seams (`notifyContext`, `mcpStdin`, `mcpStdout`);
- the probe-matrix and pipe-client tests.

asn-lookup is family A's reference migration (mcptool's migration policy §7).

## Decision

This follows mcptool's migration policy (`docs/en/migration.md`, v0.1.3). What applies here:

1. **Registering tools (policy §2.1)**:
   - `lookup_ip` and `lookup_asn` are registered with their argument structs through `mcptool.Add`;
   - `get_usage`, `update_db` and `db_status` through `AddNoArgs`; today's schemas carry an empty `properties: {}`, which stays;
   - each tool's schema and struct name the same set (registration checks it).
2. **Handlers and errors (policy §2.2)**: the failures answered as plain text get codes, in `internal/mcp/codes.go`.

| Failure | Code | details |
|---|---|---|
| `lookup_ip` with neither `ip` nor `ips` | `missing_argument` | `one_of: ["ip","ips"]` |
| `lookup_asn` with neither `asn` nor `asns` | `missing_argument` | `one_of: ["asn","asns"]` |
| no database | `no_database` | — |
| the database cannot be read | `database_error` | — |
| `update_db` without an ipinfo token | `no_token` | — |
| `update_db` failing to fetch or rebuild | `update_failed` | — |

   - An unknown name or a wrong type is `invalid_arguments`, a missing required argument `missing_argument` (mcptool).
   - The messages keep today's sentences (`no_database`'s prompt to call `update_db` included).
3. **Serving (policy §2.3)**: `cmdMCP` builds the server with `mcp.New(e, version)` and runs it with `serve.Run`. `notifyContext`, `mcpStdin` and `mcpStdout` go; the signal test uses `serve.NotifyContext`, and streams pass through `serve.Options`.
4. **Tests (policy §5)**:
   - tool tests are rewritten on `tooltest`;
   - one `tooltest.ProbeMatrix` and one `tooltest.SchemaContract` (no tool returns a path);
   - `strict_args_test.go`'s findings (a misspelt name refused by name, a wrong type refused, another case refused, a refused call doing nothing) are kept, on `tooltest`;
   - before and after are compared with `mcpsnap` (policy §5).
5. **Dependency**: `github.com/nlink-jp/mcptool` v0.1.3, whose `tooltest.OpenWith` the tests use to open a server with client options (the floor is v0.1.1, ADR-031 §3).
6. **Version**: a minor bump (v0.5.0).

## Changes clients will see (from mcptool's design §5.1)

- A value's JSON is one line (no indentation); the value is the same.
- `<`, `>` and `&` arrive unescaped — an AS name's `&` arrives as `&`, not `\u0026`.
- An argument error is `{code, message, details}` JSON instead of plain text (`arguments: json: unknown field "…"`): `invalid_arguments`, the name in `details.argument`, worded `unknown argument "…"` or `argument "…" must be …`.
- With several problems in one call, the first one reported may change (unknown → missing → type).
- The failures answered as plain text are the table's codes, as JSON.
- The manual's (`usage.md`) recovery table is rewritten to be read by code, not by wording.

These changes share one commit: each follows from swapping the shell for mcptool, and none can be had without the others.

## Defects found (closed by the migration)

- `jsonNames` did not follow embedded structs: an embedded field used in arguments would have been refused as unknown (latent; today's argument types embed nothing). mcptool's registration promotes embedded fields.
- Errors had no codes: callers could only tell them apart by wording.
- `lookup_asn` answered `"prefixes": null` for an ASN it did not find (or could not parse), where a found one has an array. It now answers `[]` (visible; fixed in its own commit).

## Consequences

- `internal/mcp` keeps only the tools, their schemas, the list of codes and the database cache.
- A defect in a convention is fixed once in mcptool and reaches this server by a floor raise.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Keep today's shell and call mcptool inside it | The copy stays, and ADR-031 §6's check (no `ToolServer` built outside mcptool) would not pass |

## References

- Organization ADR-031, ADR-030, ADR-021 §4
- nlink-jp/mcptool v0.1.3: `docs/en/migration.md`, `docs/en/design.md` §5.1
- ADR-0001
- Issue nlink-jp/asn-lookup#2
