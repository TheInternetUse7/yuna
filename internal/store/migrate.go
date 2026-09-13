package store

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migrations are plain SQL files named <version>_<description>.sql, applied in
// ascending version order and recorded in schema_migrations. Adding a feature
// that needs a new table or column means dropping in the next numbered file;
// existing databases pick it up on the next boot.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

const migrationsTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    TEXT PRIMARY KEY,
  applied_at INTEGER NOT NULL
);`

type migration struct {
	version string
	name    string
	body    string
}

// applyMigrations brings the database up to date and reports nothing to do when
// it already is.
func (s *Store) applyMigrations() error {
	if _, err := s.db.Exec(migrationsTableSQL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := s.appliedMigrations()
	if err != nil {
		return err
	}
	pending, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range pending {
		if applied[m.version] {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appliedMigrations() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyMigration runs one file inside a transaction. SQLite makes DDL
// transactional, so a failure part way through leaves nothing behind and the
// version is not recorded, meaning the next boot retries it.
func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.body); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().Unix()); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	return tx.Commit()
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	seen := make(map[string]string)
	var out []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		version := migrationVersion(entry.Name())
		if version == "" {
			return nil, fmt.Errorf("migration %q must be named <number>_<description>.sql, e.g. 00001_init.sql",
				entry.Name())
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %s", other, entry.Name(), version)
		}
		seen[version] = entry.Name()
		out = append(out, migration{version: version, name: entry.Name(), body: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// migrationVersion extracts the numeric prefix, zero-padded so lexical and
// numeric order agree.
func migrationVersion(filename string) string {
	base := strings.TrimSuffix(filename, ".sql")
	if i := strings.IndexByte(base, '_'); i >= 0 {
		base = base[:i]
	}
	if _, err := strconv.Atoi(base); err != nil {
		return ""
	}
	return base
}
