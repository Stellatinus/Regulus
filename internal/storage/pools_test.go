package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// On SQLite the hub reads on a pool of its own (#219): a read does not wait
// for a write transaction, sees only committed rows, and cannot write.
func TestSQLiteReadPoolDoesNotWaitForTheWriter(t *testing.T) {
	if storagetest.Postgres() {
		t.Skip("SQLite only: on PostgreSQL Read is the (multi-connection) write pool")
	}
	ctx := context.Background()
	db, err := storage.OpenHub(ctx, storage.Config{Type: storage.TypeSQLite, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if db.Read == db.Write {
		t.Fatal("no separate read pool")
	}

	tx, err := db.Write.BeginTx(ctx, nil) // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES ('uncommitted', 'x')`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var v string
	err = db.Read.QueryRowContext(rctx, `SELECT value FROM settings WHERE key = 'uncommitted'`).Scan(&v)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read during a write transaction: err = %v, want no rows (uncommitted)", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the read waited %s for the writer", d)
	}

	if _, err := db.Read.ExecContext(rctx, `INSERT INTO settings (key, value) VALUES ('via-reader', 'x')`); err == nil {
		t.Error("the read pool accepted a write")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Read.QueryRow(`SELECT value FROM settings WHERE key = 'uncommitted'`).Scan(&v); err != nil {
		t.Errorf("committed row not visible to the read pool: %v", err)
	}
}

func TestSQLiteReadPoolCanBeTurnedOff(t *testing.T) {
	if storagetest.Postgres() {
		t.Skip("SQLite only")
	}
	db, err := storage.OpenHub(context.Background(), storage.Config{Type: storage.TypeSQLite, DataDir: t.TempDir(), ReadConns: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if db.Read != db.Write {
		t.Error("ReadConns -1 must share the writer")
	}
}
