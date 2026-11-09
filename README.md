# TWStockAnalysis-SignalNotifier

台股訊號分析網站的每日自訂訊號 Telegram 通知任務，使用 Go。
每天在 TWStockAnalysis 算完指標後執行一次：讀取使用者的自訂訊號設定與最愛清單，判斷當日是否觸發，透過使用者 bot 發送 Telegram 訊息，並把發送紀錄寫入 `user_signal_notifications`（同一使用者、股票、交易日、方向只會發一次）。每次跑完即結束，不是常駐服務。

判斷規則與訊息格式以 spec 為準：`TWStockAnalysis-AccountService/docs/superpowers/specs/2026-11-09-custom-signal-design.md`。

## 需求

- Go 1.25+
- PostgreSQL（與 AccountService、TWStockAPI 共用同一個 DB）

## 設定

```bash
cp .env.example .env
# 填入 DATABASE_URL / TELEGRAM_USER_BOT_TOKEN / SITE_BASE_URL
```

| 變數 | 必填 | 說明 |
|------|------|------|
| `DATABASE_URL` | 是 | PostgreSQL 連線字串 |
| `TELEGRAM_USER_BOT_TOKEN` | 是 | 發送使用者通知的 bot token；必須與 AccountService 綁定使用者時用的是同一個 bot（DEV 與 PROD 使用不同的 bot） |
| `SITE_BASE_URL` | 是 | 網站網址，用於訊息中的連結 |
| `TELEGRAM_BOT_TOKEN` | 否 | 管理者 bot token；與 `TELEGRAM_CHAT_ID` 都設定才會發每日摘要與告警 |
| `TELEGRAM_CHAT_ID` | 否 | 接收摘要與告警的 chat id |

所有環境變數會先去除前後空白，純空白視為未設定。程式不會把 token 或 `DATABASE_URL` 寫進 log。

## 資料庫

套用順序（皆可重複執行）：

```bash
# 1. AccountService：users 表
psql "$DATABASE_URL" -f <AccountService>/db/schema.sql
# 2. TWStockAPI：最愛與自訂訊號設定
psql "$DATABASE_URL" -f <TWStockAPI>/db/user_favorites.sql
psql "$DATABASE_URL" -f <TWStockAPI>/db/user_custom_signals.sql
# 3. 本專案：user_signal_notifications 發送紀錄表
psql "$DATABASE_URL" -f db/schema.sql
```

## 執行

```bash
go run .               # 本地
railway run go run .   # 透過 Railway
go test ./...          # 測試（store 測試會連 .env 的 DB，並在 transaction 中 rollback）
```

## 部署（Railway Cron）

以 `Dockerfile` 建置，服務設為 Cron 排程。排程時間由使用者在 Railway 設定，須排在 RawData 抓完、TWStockAnalysis 算完指標之後。每次執行完就結束。

## Exit code

| 情況 | exit code |
|------|-----------|
| 完整跑完（即使部分訊息發送失敗） | 0 |
| 取不到 advisory lock（前一次仍在執行，log `previous run still in progress`） | 0 |
| 指標尚未就緒 | 1（若有設定管理者 bot，會發送告警） |
| DB / 設定錯誤、缺少必填環境變數 | 1 |

## 補跑

指標尚未就緒而 exit 1 時，等 TWStockAnalysis 算完後直接重跑即可。已發送過的（使用者、股票、交易日、方向）不會重複發送，所以任何時候重跑都是安全的。
