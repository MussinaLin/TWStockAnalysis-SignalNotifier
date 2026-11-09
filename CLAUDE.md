# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

TWStockAnalysis-SignalNotifier 是台股訊號分析網站的每日自訂訊號 Telegram 通知任務（Go + PostgreSQL）。在 TWStockAnalysis 算完指標後由 Railway Cron 執行一次：載入使用者的自訂訊號設定（`user_stock_signals`，任何股票皆可，不限最愛），判斷當日觸發的訊號，用使用者 bot 發送訊息並記錄到 `user_signal_notifications`，跑完即結束。與 AccountService、TWStockAPI 共用同一個 DB。

判斷規則與訊息格式以 spec 為準：`/Users/mussina/Repo/TWStockAnalysis-AccountService/docs/superpowers/specs/2026-11-09-custom-signal-design.md`。

## Build & Run

```bash
go run .                 # local (reads .env)
go build -o /dev/null .  # 僅檢查編譯；不要輸出成 ./notifier（會與 notifier/ 套件目錄同名）
go vet ./... && go test ./...
```

必填：`DATABASE_URL`、`TELEGRAM_USER_BOT_TOKEN`（須與 AccountService 綁定用的是同一個 bot）、`SITE_BASE_URL`。選填：`TELEGRAM_BOT_TOKEN` + `TELEGRAM_CHAT_ID`（都有才發摘要與告警）。

Exit code：完整跑完 0（即使部分發送失敗）；拿不到 advisory lock 0；指標未就緒、DB/設定錯誤、缺必填 env 1。

## Architecture

- `main.go` — 進入點：載入 `.env`、檢查 env、建立 pool、取 advisory lock、組裝 `notifier.Deps`（100ms ticker 節流、可取消的 Sleep）、30 分鐘 timeout 執行；`adminNotifier` 以管理者 bot 發訊息。
- `signals/` — 訊號判斷邏輯（純函式）。
- `store/` — DB 存取（`Store`）與 advisory lock（`TryLock`）。
- `telegram/` — Telegram Bot API `sendMessage` client（含 blocked / retry-after 判斷）。
- `notifier/` — 主流程 `Run`：檢查指標就緒、載入資料、逐使用者發送、記錄、產生摘要。
- `db/` — pgxpool 與 `schema.sql`（`user_signal_notifications`）。

## Workflow

- 涉及 coding、架構規劃的任務，先討論、確認後再實作。
- 改動資料表結構或訊息格式時，同步更新 `CLAUDE.md` 與 `README.md`。
- Git commit message 不加 `Co-Authored-By:` trailer。
- PROD DB 未經使用者同意不可寫入；手動執行 `go run .` 會發送真實 Telegram 訊息，先確認。
- 不要把真實連線字串 / token 寫進 repo，範例一律用佔位字串。
