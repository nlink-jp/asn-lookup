# ADR-0001: MCP のプロトコル層を nlink-jp/mcp にする — 新旧両方の時代を話す

| 項目 | 値 |
|-------|-----|
| Status | **Accepted**（2026-11-27） |
| Date | 2026-11-27 |
| Binds | asn-lookup |
| Decision makers | nlink-jp maintainers |
| Triggered by | 組織 ADR-030（MCP の実装は新旧両方の時代を話し、プロトコル層は nlink-jp/mcp だけから取る）。issue nlink-jp/asn-lookup#1 |

## Context

`internal/mcp/server.go` は手書きの stdio MCP サーバだった。旧方式（`initialize` の握手）だけを話すので、
2026-07-28 版の新方式専用のクライアントからはつながらない。組織の自前サーバ 32 本は、同じ手書きの層を写して
分かれており、asn-lookup は家系 A（lookup 1 系統）の参照実装にあたる（nlink-jp/mcp の文書 server-migration §1）。

**実際の相手（2026-11-27 に測り直した。ADR-030 §7）**

- Claude Code 2.1.311 は両対応である。旧方式のみの探りのサーバには `server/discover`（2026-07-28）を送り、`-32601` を受けて `initialize`（2025-11-25）へ戻った。両対応と新方式のみのサーバとは、2026-07-28 で `tools/list` まで進んだ。
- gem-agent v0.110.0 と lagent v0.34.0 も両対応である（同日に移行してリリースした。探りの組み合わせの試験と、登録した 30 本での実機の確認による）。

## Decision

共通の方針（nlink-jp/mcp の `docs/ja/server-migration.ja.md`、v0.3.0）に従う。そのうえで、このリポに当てはまる
項目を次のように決める。

1. **外から見える形は変えない（方針 §2）。** `mcp.Serve(ctx, e, version, in, out)` の形を残す。中身は
   `server.ToolServer` で、`toolsList()` の順に登録し、各ツールのハンドラから `toolsCall(ctx, name, args)` を呼ぶ。
   手書きの読み書きのループ・握手・`rpcError` は消す。`toolsCall` は params の解釈をやめ、名前と引数を受け取る
   （params の解釈と未知のツールは、モジュールが先に扱う）。
2. **選択肢（方針 §2）**: `Workers` は 1（DB のキャッシュがロックを持たないため）、`QueueDepth` は 256。ログは
   stderr への slog（Warn 以上）。名前・版・instructions はいまのまま渡す。
3. **ツールの失敗（方針 §3）**: `decodeArgs` による厳格な解釈と、isError の文はいまのまま。
4. **変わる振る舞い（方針 §4）**: initialize の前の `tools/*` は -32602 になる。オブジェクトでない
   `arguments` は -32602 になる。`"id":null` は -32600 になる。枠付けは `json.Decoder` から 1 行 1 件（16 MiB）に
   変わる。未知のツールは、いまも -32602 なので変わらない。
5. **取り消し（方針 §5）**: `update_db` は取り消されると止まるようになる。DB は一時ファイルに書いてから rename
   しており（`engine.Update`）、途中で止まっても半端なファイルは残らない。
6. **依存**: `github.com/nlink-jp/mcp` を足す。「依存ゼロ・標準ライブラリのみ」の記述は、「標準ライブラリと、同じ
   規則で書かれた nlink-jp のモジュール（組織 ADR-030）」に改める。
7. **版**: minor を上げる（v0.4.0）。

## Consequences

- 新方式専用のクライアントからもつながる。旧方式のクライアントには、これまでどおり答える。
- プロトコル層の更新はモジュールから届く。
- 試験はモジュールの `client` で動かす。プロトコルの細かな振る舞い（parse error・未知メソッド・版の交渉）の試験は外した（モジュールが試験している）。探りのクライアント 3 種の組み合わせを足した。

## Alternatives considered

| 案 | 採らない理由 |
|---|---|
| 手書きの層に `server/discover` を足す | ADR-030 §6 が禁じる、握手の写しが残る |
| `Serve` の形もモジュールに合わせる | 呼び出し側まで変わるのに、得るものが無い |
| `QueueDepth` を既定の 16 にする | 並べて送られた 17 件目が「server busy」で断られる。移行前は待たせていた |

## References

- 組織 ADR-030、ADR-025
- nlink-jp/mcp v0.3.0: `docs/ja/server-migration.ja.md`、詳細設計 §3.5
- issue nlink-jp/asn-lookup#1
