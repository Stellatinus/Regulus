package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Errors of OpenExisting about the schema version.
var (
	// ErrSchemaNewer: the database has migrations this binary does not know.
	ErrSchemaNewer = errors.New("it was created by a newer version")
	// ErrSchemaOlder: the database lacks migrations this binary has, so the
	// hub that would apply them has not started on it yet.
	ErrSchemaOlder = errors.New("the database has not been migrated to this version of Regulus yet")
	// ErrNoDatabase: there is no hub database where the settings point.
	ErrNoDatabase = errors.New("no hub database")
)

// OpenExisting opens the database named by cfg for a tool that runs beside
// the hub (regulus hub reset-admin) and must not change its schema. It never
// creates a SQLite file and never migrates: migrating is the job of the hub,
// and a tool run after a package upgrade but before the service restart
// would otherwise migrate the database under the old hub that still uses it.
// It refuses a database whose schema is not exactly the one this binary
// knows: an older one (ErrSchemaOlder: restart the hub first, it migrates),
// a newer one (ErrSchemaNewer) or none at all (ErrNoDatabase).
func OpenExisting(ctx context.Context, cfg Config) (*sql.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.IsPostgres() {
		path := filepath.Join(cfg.DataDir, FileName)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%w at %s: check --data-dir (or data_dir in the configuration)", ErrNoDatabase, path)
			}
			return nil, fmt.Errorf("cannot read %s: %w", path, err)
		}
	}
	db, err := openPool(cfg)
	if err != nil {
		return nil, err
	}
	if err := checkExisting(ctx, db, cfg); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func checkExisting(ctx context.Context, db *sql.DB, cfg Config) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to database: %s", Explain(err))
	}
	if cfg.IsPostgres() {
		if _, err := verifySchema(ctx, db, cfg); err != nil {
			return err
		}
	}
	d := dialects[backendType(cfg)]
	migrations, err := loadMigrations(migrationFiles, d.dir)
	if err != nil {
		return err
	}
	exists, err := hasMigrationsTable(ctx, db, d)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w in %s: check the database settings", ErrNoDatabase, cfg.Describe())
	}
	applied, err := appliedVersions(ctx, db, d)
	if err != nil {
		return err
	}
	if err := refuseUnknown(applied, migrations); err != nil {
		return err
	}
	for _, m := range migrations {
		if _, ok := applied[m.version]; !ok {
			return fmt.Errorf("%w (migration %04d_%s is missing): restart the hub (systemctl restart regulus), which applies it, then run this again", ErrSchemaOlder, m.version, m.name)
		}
	}
	return nil
}

// hasMigrationsTable reports whether schema_migrations exists, without
// creating it.
func hasMigrationsTable(ctx context.Context, db *sql.DB, d dialect) (bool, error) {
	q := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`
	if d.postgres {
		q = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'schema_migrations'`
	}
	var n int
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return false, fmt.Errorf("read the schema: %s", Explain(err))
	}
	return n > 0, nil
}
