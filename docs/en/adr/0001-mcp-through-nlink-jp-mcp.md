# ADR-0001: The MCP protocol layer becomes nlink-jp/mcp — both eras

| Field | Value |
|-------|-------|
| Status | **Accepted** (2026-11-27) |
| Date | 2026-11-27 |
| Binds | asn-lookup |
| Decision makers | nlink-jp maintainers |
| Triggered by | Organization ADR-030 (every MCP implementation speaks both eras and takes its protocol layer only from nlink-jp/mcp); issue nlink-jp/asn-lookup#1 |

## Context

`internal/mcp/server.go` was a hand-written stdio MCP server. It spoke the legacy era only (the `initialize`
handshake), so a modern-only client of the 2026-07-28 revision could not reach it. The organization's 32 in-house
servers branched from copies of the same hand-written layer; asn-lookup is the reference implementation of family A
(lookup, first line; nlink-jp/mcp's server-migration §1).

**Real peers (measured again on 2026-11-27; ADR-030 §7)**

- Claude Code 2.1.311 is dual-era. Against a legacy-only probe server it sent `server/discover` (2026-07-28),
  took the `-32601` and fell back to `initialize` (2025-11-25). With the dual-era and modern-only probe servers it
  went on in 2026-07-28 to `tools/list`.
- gem-agent v0.110.0 and lagent v0.34.0 are dual-era too (migrated and released the same day; their probe-matrix
  tests and a real-peer check against the 30 registered servers).

## Decision

> Decisions 1 (`mcp.Serve` keeps its shape) and 3 (`decodeArgs`, the plain-text isError) are superseded by ADR-0002: the server runs through `mcp.New` and mcptool's `serve.Run`, mcptool decodes the arguments, and failures are JSON with codes.

Follow the common policy (nlink-jp/mcp's `docs/en/server-migration.md`, v0.3.0), with these items for this
repository:

1. **The shape seen from outside stays (policy §2).** `mcp.Serve(ctx, e, version, in, out)` keeps its shape. Inside
   it is a `server.ToolServer`: the tools are registered in `toolsList()` order and each handler calls
   `toolsCall(ctx, name, args)`. The hand-written read/write loop, handshake and `rpcError` go. `toolsCall` no
   longer parses params and takes the name and the arguments (the module handles params and unknown tools first).
2. **Options (policy §2)**: `Workers` 1 (the database cache has no lock), `QueueDepth` 256, a `slog` to stderr
   (Warn and above). Name, version and instructions as today.
3. **Tool failures (policy §3)**: strict decoding through `decodeArgs` and the isError texts stay.
4. **Behaviour that changes (policy §4)**: `tools/*` and unknown methods before initialize are -32602 (an unknown
   method was -32601); `arguments` that is not an object is -32602; `"id":null` and a request without `"jsonrpc"` are
   -32600; framing changes from `json.Decoder` to one message per line (16 MiB); a parse error or a batch no longer
   ends the server (a batch gets -32600); a panic in a tool is answered -32603 and the server goes on (it crashed);
   up to 256 calls wait behind a running one, more get -32603 "server busy". An unknown tool was -32602 already and
   stays so.
8. **Signals (policy §2)**: the `mcp` subcommand passes `Serve` a ctx cancelled by SIGINT and SIGTERM.
5. **Cancellation (policy §5)**: `update_db` now stops when cancelled. The database is written to a temporary file
   and renamed (`engine.Update`), so a stop midway leaves no partial file.
6. **Dependency**: `github.com/nlink-jp/mcp` is added. "Zero dependencies / standard library only" becomes "the
   standard library and nlink-jp modules written on the same rule (organization ADR-030)".
7. **Version**: minor (v0.4.0).

## Consequences

- Modern-only clients reach it too; legacy clients are answered as before.
- Protocol-layer updates arrive through the module.
- Tests run through the module's `client`. Tests of protocol details (parse errors, unknown methods, version
  negotiation) went (the module tests them); a test against the three probe clients was added.

## Defects found (each fixed in its own fix commit)

- Argument names were matched case-insensitively (`{"ASN":...}` passed as `asn`). They are now matched exactly, as in
  cert-lookup and vpn-lookup.
- `mcp` served on `context.Background()`, so a SIGTERM during `update_db` ended the process with `asndb-*.tmp` left
  beside the database (measured with v0.3.2). Signals now cancel the ctx.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Add `server/discover` to the hand-written layer | Leaves the copy of the handshake that ADR-030 §6 forbids |
| Change `Serve`'s shape to the module's | The callers change too, for nothing gained |
| Keep `QueueDepth` at the default 16 | The 17th call sent side by side would be refused as "server busy"; before the move it waited |

## References

- Organization ADR-030, ADR-025
- nlink-jp/mcp v0.3.0: `docs/en/server-migration.md`, detailed design §3.5
- Issue nlink-jp/asn-lookup#1
