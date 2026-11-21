package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string

	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

func LoadConfigFromEnv() Config {
	port, _ := strconv.Atoi(os.Getenv("FAIRPASS_DB_PORT"))
	if port == 0 {
		port = 5432
	}

	maxConns, _ := strconv.Atoi(os.Getenv("FAIRPASS_DB_MAX_CONNS"))
	if maxConns == 0 {
		maxConns = 25
	}

	minConns, _ := strconv.Atoi(os.Getenv("FAIRPASS_DB_MIN_CONNS"))
	if minConns == 0 {
		minConns = 5
	}

	sslMode := os.Getenv("FAIRPASS_DB_SSLMODE")
	if sslMode == "" {
		sslMode = "disable"
	}

	dbName := os.Getenv("FAIRPASS_DB_NAME")
	if dbName == "" {
		dbName = "fairpass_mirror"
	}

	host := os.Getenv("FAIRPASS_DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	user := os.Getenv("FAIRPASS_DB_USER")
	if user == "" {
		user = "postgres"
	}

	return Config{
		Host:            host,
		Port:            port,
		User:            user,
		Password:        os.Getenv("FAIRPASS_DB_PASSWORD"),
		DBName:          dbName,
		SSLMode:         sslMode,
		MaxConns:        int32(maxConns),
		MinConns:        int32(minConns),
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func NewConnectionPool(ctx context.Context, cfg Config) (*DB, error) {
	connString := os.Getenv("DATABASE_URL")

	if connString == "" {
		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(cfg.User, cfg.Password),
			Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
			Path:   "/" + cfg.DBName,
		}

		q := u.Query()
		q.Set("sslmode", cfg.SSLMode)
		u.RawQuery = q.Encode()

		connString = u.String()
	}

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database configuration: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database ping check failed: %w", err)
	}

	log.Printf("Connected to local PostgreSQL database '%s' at %s:%d", cfg.DBName, cfg.Host, cfg.Port)
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return errors.New("database connection is nil")
	}
	return db.Pool.Ping(ctx)
}

func (db *DB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if db == nil || db.Pool == nil {
		return errors.New("database connection is nil")
	}

	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
