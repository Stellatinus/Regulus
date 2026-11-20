package hub

import (
	"os"
	"testing"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// useTestDatabase points the hub of a test at PostgreSQL when
// REGULUS_TEST_DATABASE_URL is set, so the whole hub runs on both backends.
func useTestDatabase(t *testing.T, cfg *config.Config) {
	t.Helper()
	if !storagetest.Postgres() {
		return
	}
	s := storagetest.Config(t, cfg.DataDir)
	cfg.Database = config.Database{
		Type: s.Type, Host: s.Host, Port: s.Port, Name: s.Name, User: s.User,
		Password: s.Password, SSLMode: s.SSLMode, Schema: s.Schema,
	}
}

// EnvOneConnection makes every hub of the tests use one SQLite connection for
// reads and writes, as before the read pool existed (#219). Code that queries
// the database while a transaction or a result set of its own holds that
// connection then hangs, and the test times out: `make test-one-connection`
// and CI run the hub tests this way.
const EnvOneConnection = "REGULUS_TEST_ONE_CONNECTION"

func init() {
	if os.Getenv(EnvOneConnection) != "" {
		tuneStorage = func(c *storage.Config) { c.ReadConns = -1 }
	}
}
