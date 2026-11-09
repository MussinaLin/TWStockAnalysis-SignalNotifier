package db

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func InitPool() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MinConns = 2
	cfg.MaxConns = 10

	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	pool = p
	return nil
}

func ClosePool() {
	if pool != nil {
		pool.Close()
	}
}

func Pool() *pgxpool.Pool {
	return pool
}
