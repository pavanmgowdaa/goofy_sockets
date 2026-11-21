package db

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		return nil, e
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return p, nil
}
