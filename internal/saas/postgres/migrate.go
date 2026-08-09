// Package postgres owns the hosted PostgreSQL connection and schema lifecycle.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Migration struct {
	Version string
	Up      string
	Down    string
}

func Open(ctx context.Context, connectionURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, strings.TrimSpace(connectionURL))
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}

func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	byVersion := make(map[string]*Migration)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".sql")
		version, direction, ok := strings.Cut(name, ".")
		if !ok || (direction != "up" && direction != "down") {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		migration := byVersion[version]
		if migration == nil {
			migration = &Migration{Version: version}
			byVersion[version] = migration
		}
		if direction == "up" {
			migration.Up = string(contents)
		} else {
			migration.Down = string(contents)
		}
	}
	versions := make([]string, 0, len(byVersion))
	for version, migration := range byVersion {
		if strings.TrimSpace(migration.Up) == "" || strings.TrimSpace(migration.Down) == "" {
			return nil, fmt.Errorf("migration %q must have up and down files", version)
		}
		versions = append(versions, version)
	}
	sort.Strings(versions)
	result := make([]Migration, 0, len(versions))
	for _, version := range versions {
		result = append(result, *byVersion[version])
	}
	return result, nil
}

func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("PostgreSQL pool is required")
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS saas_schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
	)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	migrations, err := Migrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		var applied bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM saas_schema_migrations WHERE version = $1)`, migration.Version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", migration.Version, err)
		}
		if applied {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.Version, err)
		}
		if _, err = tx.Exec(ctx, migration.Up); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO saas_schema_migrations (version) VALUES ($1)`, migration.Version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", migration.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.Version, err)
		}
	}
	return nil
}

func RollbackLatest(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("PostgreSQL pool is required")
	}
	var version string
	err := pool.QueryRow(ctx, `SELECT version FROM saas_schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version)
	if err != nil {
		return fmt.Errorf("select latest migration: %w", err)
	}
	migrations, err := Migrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if migration.Version != version {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, migration.Down); err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM saas_schema_migrations WHERE version = $1`, version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("rollback migration %s: %w", version, err)
		}
		return tx.Commit(ctx)
	}
	return fmt.Errorf("embedded migration %s not found", version)
}
