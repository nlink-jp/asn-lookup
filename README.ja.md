# asn-lookup

[IPinfo Lite](https://ipinfo.io/lite) データベースを使った、ローカルの IP↔AS
ルックアップツール。CLI **兼** ローカル MCP サーバーです。無料の IPinfo Lite DB
を一度ダウンロードしてコンパクトなローカル索引を構築し、以降は完全オフラインで
照会します。AI ツールが ipinfo API を過度に叩かずに IP を調査できるようになります。

- **IP → AS**: IP から ASN・AS 名・AS ドメイン・国/大陸を返します。
- **AS → IP**: ASN からその ASN が広告する IP プレフィックス一覧を返します。
- **CLI + MCP**: 同一エンジンが対話 CLI と stdio MCP サーバー
  （`lookup_ip` / `lookup_asn` / `update_db` / `db_status`）を駆動します。
- **外部依存ゼロ**: 標準ライブラリのみ（`net/netip` + 自前オンディスク索引）。
  MMDB リーダーも設定ライブラリも使いません。

> **データ:** IPinfo Lite（<https://ipinfo.io/lite>）、ライセンスは
> [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/)。IPinfo への
> クレジット表記が**必須**です。本ツールは各自のトークンで DB をダウンロードし、
> DB 自体は再頒布しません。

## インストール

```bash
make build          # → dist/asn-lookup（macOS では署名付き）
```

Go 1.25 以上が必要です。全プラットフォーム向けは `make build-all`。

## クイックスタート

1. <https://ipinfo.io/signup> で**無料**トークンを取得（カード不要）。
2. DB をダウンロードしてローカル索引を構築:

   ```bash
   export IPINFO_TOKEN=your_token_here
   asn-lookup update
   ```

3. 照会:

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

## コマンド

| コマンド | 説明 |
|---------|------|
| `asn-lookup ip <IP>...` | IP ごとに AS + 国/大陸を照会。引数なしなら stdin から読む。 |
| `asn-lookup asn <ASN>...` | ASN が広告するプレフィックス一覧（`AS15169` / `15169`）。引数なしなら stdin。 |
| `asn-lookup update` | IPinfo Lite をダウンロードしローカル索引を再構築（アトミック置換）。 |
| `asn-lookup doctor` | DB の有無・鮮度・設定を診断。 |
| `asn-lookup mcp` | stdio でローカル MCP サーバーとして起動。 |
| `asn-lookup version` | バージョンとデータ帰属表記を表示。 |

### 出力形式

- 既定: 人間向けの整形テーブル。
- `-j` / `--json`: **JSON Lines**（入力1件につき1 JSON オブジェクト）。パイプ向け。

```bash
cat ips.txt | asn-lookup ip --json | jq 'select(.found) | .asn'
```

不明・未マッピング・不正な入力は行ごとに報告され、バッチ全体は中断しません。
`asn` では `--count` で件数のみ、`-n/--limit N` で表示プレフィックス数を制限できます。

## MCP サーバー

MCP クライアントに登録します。Claude Code の場合:

```bash
claude mcp add asn-lookup -- /path/to/asn-lookup mcp
```

クライアント設定なら:

```json
{
  "mcpServers": {
    "asn-lookup": { "command": "/path/to/asn-lookup", "args": ["mcp"] }
  }
}
```

ツール（まず `get_usage` でマニュアル/リカバリ表を取得。`initialize` の
`instructions` でも案内されます）:

| ツール | 引数 | 用途 |
|------|------|------|
| `get_usage` | — | 操作マニュアル: ツール・ワークスペースモデル・リカバリ表 |
| `lookup_ip` | `ip`（文字列）または `ips`（配列） | IP → AS + 国/大陸 |
| `lookup_asn` | `asn`/`asns`, `limit`, `format`, `workspace_root`, `workspace_id` | ASN → プレフィックス一覧（大きい結果はファイル経由） |
| `update_db` | — | DB のダウンロード+再構築（トークン必要） |
| `db_status` | — | 生成日・レコード数・鮮度 |

**大きな ASN の結果はファイル経由**です。一部の ASN は数十万のプレフィックスに
なります（例: Cloudflare は IPinfo Lite で約59万件）。`lookup_asn` は常にコンパクトな
サマリ（`prefix_count`/`v4_count`/`v6_count`）+ インライン preview を返し、全件が
`limit`（既定50）を超える場合は**ファイルに書き出してパスのみ返します**
（`prefixes_file`, `truncated: true`）。これで巨大 ASN が AI のコンテキストを溢れ
させません。voice-studio-mcp と同じく**出力先はエージェント指定**です。自分の
ファイルツールでディレクトリを作り `workspace_root` に渡してください（サンドボックス
環境では必須）。省略時はサーバー既定を使用。書き込みは `os.Root` でワークスペース内に
封じ込め（仕込まれた symlink は脱出不可）。ファイル `format`（`cidr`/`json`）は
リクエストごとに指定します。

DB は自動ダウンロードされません。CLI は `update` の実行を案内し、MCP クライアント
は `update_db` を自分で呼べます。古い DB（30 日超）は警告のみで、自動更新はしません。

## 設定

設定は次の順で解決されます（後勝ち）: 設定ファイル → 環境変数 → フラグ。

**トークン**（`update` のみ必須）:

- `IPINFO_TOKEN`（または `ASN_LOOKUP_TOKEN`）環境変数、または
- 設定ファイルの `[ipinfo] token`。

**設定ファイル** — `~/.config/asn-lookup/config.toml`
（`XDG_CONFIG_HOME` を尊重）。[`config.example.toml`](config.example.toml) 参照:

```toml
[ipinfo]
token = "your_token_here"
# lite_url = "https://ipinfo.io/data/ipinfo_lite.csv.gz"

[db]
# path = "~/.local/share/asn-lookup/asndb.bin"

[mcp]
# ファイル経由結果の既定出力先（ASN_LOOKUP_WORKSPACE）。
# 呼び出し側は workspace_root でリクエストごとに上書き可。
# workspace = "~/.local/state/asn-lookup/workspace"
```

**索引の場所** — `~/.local/share/asn-lookup/asndb.bin`
（`XDG_DATA_HOME` を尊重。`--db` で上書き可）。

トークンはログに書き出されません。エラーに現れる URL はトークンが伏せられます。

## 仕組み

`update` は gzip 圧縮の IPinfo Lite CSV をストリーム処理し、各 `network` 行を
`net/netip` で解析して、コンパクトな索引を書き出します（重複排除した AS/geo
レコード + アドレスファミリ別にソートしたレンジ）。`ip` はレンジを二分探索し、
`asn` はレンジを走査します（逆引きビューは同一データから導出されるため、前方参照
とドリフトしません）。詳細は
[docs/ja/architecture.ja.md](docs/ja/architecture.ja.md)。

## 開発

```bash
make test     # go test -race -cover ./...
make build    # → dist/asn-lookup
make check    # lint + test + build-all
```

## ライセンス

コード: [MIT](LICENSE)。データベース: IPinfo Lite, CC BY-SA 4.0 — IPinfo への
クレジット必須、本ツールでは再頒布しません。
