package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LockKey 是 notifier 單例用的 advisory lock key
const LockKey int64 = 0x5349474e414c

// TryLock 從 pool 取一條專用連線執行 pg_try_advisory_lock(LockKey)。
// ok == false 代表已有其他實例持有，連線已歸還；ok == true 時呼叫 release 會 unlock 並歸還連線。
func TryLock(ctx context.Context, pool *pgxpool.Pool) (release func(), ok bool, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, LockKey).Scan(&ok); err != nil {
		// 鎖可能已在 server 端取得，關閉連線確保 session lock 釋放
		_ = conn.Conn().Close(context.Background())
		conn.Release()
		return nil, false, err
	}
	if !ok {
		conn.Release()
		return func() {}, false, nil
	}
	return func() {
		// ctx 可能已取消，unlock 用獨立 context；失敗時連線關閉也會釋放 session lock
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(uctx, `SELECT pg_advisory_unlock($1)`, LockKey).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Conn().Close(context.Background())
		}
		conn.Release()
	}, true, nil
}
