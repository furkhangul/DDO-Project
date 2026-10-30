package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"konakpro/internal/db"
)

const (
	codeExclusion  = "23P01"
	codeUnique     = "23505"
	codeCheck      = "23514"
	codeForeignKey = "23503"
	codeDeadlock   = "40P01"
	codeSerialize  = "40001"
)

type Store struct {
	Pool *pgxpool.Pool
	*db.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{Pool: pool, Queries: db.New(pool)}
}

func Connect(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("veritabanı adresi: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnLifetime = 30 * time.Minute
	return pgxpool.NewWithConfig(ctx, cfg)
}

func (s *Store) InTx(ctx context.Context, fn func(q *db.Queries) error) error {
	const attempts = 4
	var err error
	for i := 0; i < attempts; i++ {
		err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
			return fn(s.WithTx(tx))
		})
		if !retryable(err) {
			return err
		}
		time.Sleep(time.Duration(i+1) * 15 * time.Millisecond)
	}
	return err
}

func retryable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == codeDeadlock || pgErr.Code == codeSerialize)
}

func constraint(err error, code string) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == code {
		return pgErr.ConstraintName, true
	}
	return "", false
}

func IsExclusion(err error) bool {
	_, ok := constraint(err, codeExclusion)
	return ok
}

func IsUnique(err error) bool {
	_, ok := constraint(err, codeUnique)
	return ok
}

func IsCheck(err error) bool {
	_, ok := constraint(err, codeCheck)
	return ok
}

func IsForeignKey(err error) bool {
	_, ok := constraint(err, codeForeignKey)
	return ok
}

func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
