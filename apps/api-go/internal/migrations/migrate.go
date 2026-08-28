package migrations

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

const initialVersion = "20260802000000_init"

//go:embed sql/*.sql
var migrationFiles embed.FS

func Apply(ctx context.Context, conn *pgx.Conn) (string, error) {
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS "go_schema_migrations" ("version" TEXT PRIMARY KEY, "appliedAt" TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return "", fmt.Errorf("create migration table: %w", err)
	}

	entries, err := migrationFiles.ReadDir("sql")
	if err != nil {
		return "", fmt.Errorf("read embedded migrations: %w", err)
	}
	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Strings(filenames)

	appliedCount := 0
	baselinedInitial := false
	for _, filename := range filenames {
		version := strings.TrimSuffix(filename, ".sql")
		var applied bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "go_schema_migrations" WHERE "version"=$1)`, version).Scan(&applied); err != nil {
			return "", fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied {
			continue
		}

		migrationSQL, err := migrationFiles.ReadFile("sql/" + filename)
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", version, err)
		}

		skipSQL := false
		if version == initialVersion {
			if err := conn.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&skipSQL); err != nil {
				return "", fmt.Errorf("inspect existing schema: %w", err)
			}
			baselinedInitial = skipSQL
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return "", fmt.Errorf("begin migration %s: %w", version, err)
		}
		if !skipSQL {
			if _, err := tx.Exec(ctx, string(migrationSQL)); err != nil {
				_ = tx.Rollback(ctx)
				return "", fmt.Errorf("apply migration %s: %w", version, err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO "go_schema_migrations" ("version") VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return "", fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit migration %s: %w", version, err)
		}
		appliedCount++
	}

	if appliedCount == 0 {
		return "Database is already up to date.", nil
	}
	if baselinedInitial && appliedCount == 1 {
		return "Existing Prisma schema recorded as the Go migration baseline.", nil
	}
	return fmt.Sprintf("Applied %d database migration(s).", appliedCount), nil
}
