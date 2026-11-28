# ADR-0002: ツールの作法を nlink-jp/mcptool にする

| 項目 | 値 |
|-------|-----|
| Status | **Accepted**（2026-11-28） |
| Date | 2026-11-28 |
| Binds | asn-lookup |
| Decision makers | nlink-jp maintainers |
| Triggered by | 組織 ADR-031（MCP のツールの作法を 1 つのモジュールに置き、サーバは写すのをやめる）。issue nlink-jp/asn-lookup#2 |

## Context

ADR-0001 で、プロトコル層を nlink-jp/mcp に移した。その上の作法は、このリポに写したままだった。
- `internal/mcp/server.go` の殻（`toolResult`・`textResult`・`jsonResult`・ディスパッチ）
- `decodeArgs`・`jsonNames`
- 起動の差し替え口（`notifyContext`・`mcpStdin`・`mcpStdout`）
- 探り 3 種と管の上のクライアントの試験

asn-lookup は家系 A の参照実装である（mcptool の移行の方針 §7）。

## Decision

mcptool の移行の方針（`docs/ja/migration.ja.md`、v0.1.2）に従う。このリポに当てはまる項目は次のとおり。

1. **ツールの登録（方針 §2.1）**:
   - `lookup_ip` と `lookup_asn` は、引数の構造体を `mcptool.Add` で登録する。
   - `get_usage`・`update_db`・`db_status` は `AddNoArgs` で登録する。いまのスキーマは空の `properties: {}` を持つので、そのまま残す。
   - スキーマと構造体の名前の集合は、どのツールでも一致していた（登録が確かめる）。
2. **ハンドラとエラー（方針 §2.2）**: 平文で返していた失敗に、コードを付ける。コードは `internal/mcp/codes.go` に置く。

| 失敗 | コード | details |
|---|---|---|
| `lookup_ip` に `ip` も `ips` も無い | `missing_argument` | `one_of: ["ip","ips"]` |
| `lookup_asn` に `asn` も `asns` も無い | `missing_argument` | `one_of: ["asn","asns"]` |
| データベースが無い | `no_database` | — |
| データベースが読めない | `database_error` | — |
| `update_db` に ipinfo の token が無い | `no_token` | — |
| `update_db` の取得・作り直しの失敗 | `update_failed` | — |

   - 未知の名前・型の誤りは `invalid_arguments`、必須の欠けは `missing_argument` になる（mcptool）。
   - 文言は、いまの文をそのまま使う（`no_database` の、`update_db` を呼ぶよう促す一文も含む）。
3. **起動（方針 §2.3）**: `cmdMCP` は `mcp.New(e, version)` でサーバを作り、`serve.Run` で動かす。`notifyContext`・`mcpStdin`・`mcpStdout` は消す。信号の試験は `serve.NotifyContext` の差し替え口で行い、入出力は `serve.Options` で渡す。
4. **試験（方針 §5）**:
   - ツールの試験は `tooltest` の上に書き直す。
   - `tooltest.ProbeMatrix` を 1 本と `tooltest.SchemaContract` を 1 本置く（パスを返すツールは無い）。
   - `strict_args_test.go` の所見（綴りの誤りを名前で断る、型の誤りを断る、大文字小文字の違いを断る、断られた呼び出しは何もしない）は、`tooltest` の上で残す。
   - 移行の前後は `mcpsnap` で突き合わせる（方針 §5）。
5. **依存**: `github.com/nlink-jp/mcptool` v0.1.2 を足す（下限は v0.1.1。ADR-031 §3）。
6. **版**: minor を上げる（v0.5.0）。

## 外から見える変化（mcptool の詳細設計 §5.1 から）

- 値の JSON が 1 行になる（字下げをやめる）。値は同じ。
- `<`・`>`・`&` が逃がされずに届く。たとえば AS の名前の `&` が `&` ではなく `&` で届く。
- 引数の誤りは、平文（`arguments: json: unknown field "…"`）ではなく、`{code, message, details}` の JSON になる。コードは `invalid_arguments`、`details.argument` に名前を入れる。文言は `unknown argument "…"`・`argument "…" must be …` になる。
- 誤りが重なった呼び出しで、最初に返す誤りが変わりうる（未知 → 必須 → 型の順）。
- 平文で返していた失敗は、上の表のコードの JSON になる。
- 説明書（`usage.md`）の回復の表は、文言ではなくコードで引くように書き直す。

## 見つけた不具合（移行で閉じる）

- `jsonNames` が埋め込みの構造体をたどらなかった。埋め込みの欄を引数に使えば、その名前が「未知」として断られていた（潜在。いまの引数の型には埋め込みが無い）。mcptool の登録は、埋め込みを持ち上げる。
- エラーにコードが無く、呼び出す側は文言で分けるしかなかった。
- `lookup_asn` は、見つからない（読めない）ASN に `"prefixes": null` を返していた。見つかった ASN では配列である。`[]` を返すようにする（外から見える。コミットは分ける）。

## Consequences

- `internal/mcp` には、ツール・スキーマ・コードの一覧・データベースの覚えだけが残る。
- 作法の不具合は mcptool の 1 回の修正で直り、下限を上げれば届く。

## Alternatives considered

| 案 | 採らない理由 |
|---|---|
| いまの殻を残し、中で mcptool を呼ぶ | 写しが残る。ADR-031 §6 の検査（`ToolServer` を mcptool の外で作らない）も通らない |

## References

- 組織 ADR-031、ADR-030、ADR-021 §4
- nlink-jp/mcptool v0.1.2: `docs/ja/migration.ja.md`、`docs/ja/design.ja.md` §5.1
- ADR-0001
- issue nlink-jp/asn-lookup#2
