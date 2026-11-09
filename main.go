package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"main/db"
	"main/notifier"
	"main/store"
	"main/telegram"
)

// adminNotifier 以管理者 bot 發送摘要與告警。
type adminNotifier struct {
	client telegram.Client
	chatID string
}

func (a adminNotifier) Notify(ctx context.Context, text string) error {
	return a.client.Send(ctx, a.chatID, text)
}

func env(key string) string { return strings.TrimSpace(os.Getenv(key)) }

func main() { os.Exit(run()) }

// run 回傳 exit code；defer 的清理（release、ClosePool）在 os.Exit 之前執行。
func run() int {
	_ = godotenv.Load()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	userToken := env("TELEGRAM_USER_BOT_TOKEN")
	siteBaseURL := env("SITE_BASE_URL")
	if userToken == "" || siteBaseURL == "" {
		slog.Error("missing required env: TELEGRAM_USER_BOT_TOKEN and SITE_BASE_URL")
		return 1
	}

	var admin notifier.AdminNotifier
	adminToken, adminChat := env("TELEGRAM_BOT_TOKEN"), env("TELEGRAM_CHAT_ID")
	if adminToken != "" && adminChat != "" {
		admin = adminNotifier{client: telegram.New(adminToken), chatID: adminChat}
	} else {
		slog.Info("admin bot not configured; summary and alerts will not be sent")
	}

	if err := db.InitPool(); err != nil {
		slog.Error("init db pool failed", "error", err)
		return 1
	}
	defer db.ClosePool()

	lockCtx, lockCancel := context.WithTimeout(context.Background(), 30*time.Second)
	release, ok, err := store.TryLock(lockCtx, db.Pool())
	lockCancel()
	if err != nil {
		slog.Error("acquire lock failed", "error", err)
		return 1
	}
	if !ok {
		slog.Info("previous run still in progress")
		return 0
	}
	defer release()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	deps := notifier.Deps{
		Store:  store.New(db.Pool()),
		Sender: telegram.New(userToken),
		Admin:  admin,
		Throttle: func(ctx context.Context) error {
			select {
			case <-ticker.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		SiteBaseURL: siteBaseURL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	sum, err := notifier.Run(ctx, deps)
	if err != nil {
		slog.Error("run failed", "error", err)
		return 1
	}
	slog.Info("run complete",
		"trade_date", sum.TradeDate.Format("2006-01-02"),
		"sent", sum.Sent, "failed", sum.Failed,
		"unbound", sum.Unbound, "missing_level", sum.MissingLevel)
	return 0
}
