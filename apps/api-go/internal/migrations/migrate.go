package migrations

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const initialVersion = "20260802000000_init"

//go:embed sql/20260802000000_init.sql
var initialSQL string

func Apply(ctx context.Context, conn *pgx.Conn) (string, error) {
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS "go_schema_migrations" ("version" TEXT PRIMARY KEY, "appliedAt" TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return "", fmt.Errorf("create migration table: %w", err)
	}
	var applied bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "go_schema_migrations" WHERE "version"=$1)`, initialVersion).Scan(&applied); err != nil {
		return "", err
	}
	if applied {
		return "Database is already up to date.", nil
	}

	var existingSchema bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&existingSchema); err != nil {
		return "", err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !existingSchema {
		if _, err := tx.Exec(ctx, initialSQL); err != nil {
			return "", fmt.Errorf("apply initial migration: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO "go_schema_migrations" ("version") VALUES ($1)`, initialVersion); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if existingSchema {
		return "Existing Prisma schema recorded as the Go migration baseline.", nil
	}
	return "Initial schema migration applied.", nil
}
