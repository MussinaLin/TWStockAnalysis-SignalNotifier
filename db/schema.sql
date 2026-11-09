-- SignalNotifier 擁有；可重複執行。需先套用 AccountService db/schema.sql（users）
CREATE TABLE IF NOT EXISTS user_signal_notifications (
    id         BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    symbol     VARCHAR(10) NOT NULL,
    trade_date DATE        NOT NULL,
    side       TEXT        NOT NULL CHECK (side IN ('long','short')),
    message    TEXT        NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, symbol, trade_date, side)
);
CREATE INDEX IF NOT EXISTS user_signal_notifications_trade_date_idx ON user_signal_notifications (trade_date);
