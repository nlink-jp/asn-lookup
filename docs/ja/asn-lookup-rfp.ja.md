# RFP: asn-lookup

> Generated: 2026-07-12
> Status: Draft

## 1. Problem Statement

セキュリティ調査や運用の現場では、IP アドレスから所属 AS（ASN・組織名・
ドメイン）や国・大陸を素早く知りたい、逆に特定 ASN が保有する IP プレフィックス
一覧を把握したい場面が多い。都度 ipinfo の API を叩くとレート・コスト・
オフライン性の面で問題があり、特に AI ツール（Claude Code 等）から MCP 経由で
頻繁に照会すると API コールが過大になる。

そこで **IPinfo Lite の無料ダウンロード DB をローカルに取り込み、IP↔AS の
双方向ルックアップと国/大陸情報をオフラインで即座に引ける CLI 兼ローカル MCP
サーバー** `asn-lookup` を用意する。対象ユーザーは org 運営者本人と、その AI
ツール（MCP クライアント）。これにより、AI ツールからの IP 調査を可能にしつつ、
過度な API コールを不要にする。

## 2. Functional Specification

### Commands / API Surface

単一バイナリ + サブコマンド構成（util-series 標準。`mcp` サブコマンドで MCP
サーバー化）。

| コマンド | 役割 |
|---|---|
| `asn-lookup ip <IP>...` | IP→ `{asn, as_name, as_domain, country, country_code, continent, continent_code}`。複数 IP 可、引数省略時は stdin から読む |
| `asn-lookup asn <ASN>...` | ASN→ `{as_name, as_domain, prefixes[]}`（IPv4/IPv6 プレフィックス一覧）。入力は `AS15169` / `15169` 両対応。代表国は付与しない |
| `asn-lookup update` | IPinfo Lite（`ipinfo_lite.csv.gz`）をトークンでダウンロード → ローカル索引を再構築 |
| `asn-lookup doctor` | DB の有無・鮮度（生成日）・トークン設定・索引整合性を診断 |
| `asn-lookup mcp` | ローカル MCP サーバー（stdio）として起動 |
| `asn-lookup version` | バージョン（`git describe`） |

**MCP ツール**（`asn-lookup mcp`）:

| ツール | 役割 |
|---|---|
| `lookup_ip` | IP → AS 情報 + 国/大陸 |
| `lookup_asn` | ASN → プレフィックス一覧 |
| `update_db` | DB のダウンロード/再構築を AI 自身が実行可能 |
| `db_status` | DB の鮮度・整合性を確認 |

### Input / Output

- 既定は人間向けの整形テーブル出力。`-j` / `--json` で **JSONL**（1 レコード 1 行、
  pipe / 機械処理向け）。MCP は常に構造化 JSON を返す。
- stdin 対応（`cat ips.txt | asn-lookup ip`）で util-series の pipe 哲学に沿わせる。
- **DB 未取得時の挙動**: CLI は自動ダウンロードせず、「`asn-lookup update` を
  実行してください」と案内して終了する。MCP からは `update_db` ツールで AI 自身が
  更新を実行できる。
- **鮮度警告**: DB が古い場合、`doctor` およびクエリ時に「DB は X 日前のものです」
  と警告を表示する（自動更新はしない）。

### Configuration

秘密値は非コミット。sectioned TOML を採用。

- トークン: `IPINFO_TOKEN` 環境変数 ＞ `~/.config/asn-lookup/config.toml` の
  `[ipinfo] token = "..."`
- DB / 索引の保存先: 既定 `~/.local/share/asn-lookup/`（`--db` で上書き可）

### External Dependencies

- 実行時の外部サービスは ipinfo のダウンロードエンドポイントのみ
  （`https://ipinfo.io/data/ipinfo_lite.csv.gz?token=$TOKEN`）。クエリ時はオフライン。
- Go 標準ライブラリのみ: `net/netip`（CIDR / IPv4v6 解析）、`compress/gzip`、
  `encoding/csv`、`encoding/json`。**MMDB 不使用・外部依存ゼロ**。
- 索引: `update` 時に CSV（約 357 万行）を解析し、前方参照用のソート済みレンジ配列
  + 逆引き用 `asn→prefixes` を**コンパクトなオンディスク索引**として生成 →
  クエリ時は mmap で高速検索（CLI 一発起動の読込コストを回避、MCP 常駐でも同一機構）。

## 3. Design Decisions

- **Go 採用**: util-series 標準・単一バイナリ配布。`net/netip` で CIDR / IPv4v6 を
  標準ライブラリだけで扱える。macOS 署名 + notarize フロー確立済み。
- **外部依存ゼロ**: nlk / mcp-guardian と同方針。MMDB（`oschwald/maxminddb-golang`）
  を避ける。そもそも **逆引き（AS→IP）は MMDB では実現できず** CSV 取り込みが必須
  となるため、MMDB を使う利点が消える。よって CSV 自前索引に一本化する。
- **補完する既存ツール**: cybersecurity-series（ai-ir2 / ioc-collector / cti-graph /
  mail-analyzer）の IOC エンリッチメント基盤。ioc-collector が集めた IP に AS/国を
  付与する用途など。汎用 util として AI ツール（MCP）からも利用。
- **Out of scope**:
  - 都市・緯度経度など詳細 geolocation（IPinfo Lite に含まれない）
  - 有料 DB / API のリアルタイム照会（ローカル DB 一本に絞る）
  - whois / RDAP / BGP ルーティングの実測、reverse DNS / PTR
  - **DB の再頒布**（各自トークンでダウンロード＝CC BY-SA の share-alike を回避し
    ライセンス順守）

## 4. Development Plan

### Phase 1: Core

- `net/netip` ベースの CIDR パーサ、gzip CSV 取り込み、前方参照索引（ソート済み
  レンジ + 二分探索）、逆引き索引（`asn→prefixes`）、オンディスク索引の生成/mmap
  ロード。
- `ip` / `asn` / `update` / `doctor` サブコマンド。
- pure 関数中心・注入可能な依存（ダウンロードは interface 化してテストでモック）。
  実データ小サンプルでテスト。
- **独立レビュー可**。

### Phase 2: Features

- `mcp` サブコマンド（stdio MCP: `lookup_ip` / `lookup_asn` / `update_db` /
  `db_status`）。data-toolbox-mcp の骨格を移植。
- JSONL 出力・stdin 対応の仕上げ、鮮度警告、`--db`。
- **独立レビュー可**。

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md 整備。
- **ipinfo クレジット表記（CC BY-SA 4.0）** を出力および README に明記。
- Makefile（`make build` / `make build-all`）、署名 + notarize、GitHub リリース、
  umbrella submodule ポインタ更新、org profile 追加、`check-org.sh`。

## 5. Required API Scopes / Permissions

- ipinfo 無料アカウントの**ダウンロードトークン 1 個**のみ。
- OAuth・特権 IAM ロール不要。トークンは DB ダウンロードにのみ使用する。

## 6. Series Placement

Series: util-series
Reason: pipe-friendly な照会 / 変換 CLI であり単体で完結する。cybersecurity 寄りの
用途（IOC エンリッチ）もあるが、汎用のネットワーク情報ユーティリティとして
util-series が最も適切。ユーザー確定済み。

## 7. External Platform Constraints

- IPinfo Lite は **Creative Commons Attribution-ShareAlike 4.0（CC BY-SA 4.0）**。
  → 出力および README に ipinfo へのクレジットを必須で表記し、DB そのものは
  再頒布しない。
- 日次更新・規模は約 357 万行（IPv4 + IPv6、2026-07-10 時点）。
- ダウンロードエンドポイントのレート / 帯域は非公表 → `update` は日次程度を想定し、
  頻繁なダウンロードは不要。クエリはオフラインのため実行時レート制限とは無関係。
- ダウンロードには無料トークンが必須。

---

## Discussion Log

- **着想**: ipinfo API の過大コール回避と、AI ツール（MCP）からのオフライン IP 調査を
  両立するローカルツールとして発案。
- **データソース調査**: ipinfo 公式ドキュメントを確認し、無料の **IPinfo Lite**
  ダウンロード DB（MMDB / CSV / JSON / Parquet、日次更新、CC BY-SA 4.0、フィールド:
  `network(CIDR)` / `country` / `country_code` / `continent` / `continent_code` /
  `asn` / `as_name` / `as_domain`）が要件に合致すると判断。
- **MMDB 不採用の決定**: 要件②（ASN→IP プレフィックス逆引き）は IP キーの MMDB
  では実現不可。逆引きには CSV 取り込みが必須であり、CSV を取り込むなら前方参照も
  同一データから賄えるため、MMDB を併用せず CSV 自前索引に一本化。これは
  util-series の「外部依存ゼロ」方針とも合致（`net/netip` で CIDR/v4v6 を標準
  ライブラリだけで処理）。
- **初版スコープ**: IP↔AS の双方向に加え、同一 DB に無料で含まれる国/大陸情報も
  出力に含める（追加コストほぼゼロ）。
- **`update` 挙動**: CLI は自動ダウンロードせず案内表示のみ（不意の通信を避ける）。
  一方 MCP には `update_db` ツールを用意し、AI 自身が DB を更新できるようにする。
- **鮮度**: 古い DB はクエリ時と `doctor` で警告表示（自動更新はしない）。
- **ASN 逆引きの国情報**: ASN は複数国にまたがり誤解を招くため、代表国は付与しない。
- **ツール名**: `-lens` 系（claude-usage-lens / active-lens）も候補に挙がったが、
  用途が明快に伝わる `asn-lookup` に決定。
- **系列**: util-series に確定。
