package hub

import (
	"bytes"
	"context"
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/backup"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// A hub restored from a backup keeps its users and the agents enrolled before
// the backup, without new tokens: the CA, the secret key and the certificate
// serials came back (#408).
func TestRestoredHubKeepsItsUsersAndEnrolledAgents(t *testing.T) {
	if storagetest.Postgres() {
		t.Skip("the restore here is into a SQLite data directory (the backup package tests both backends)")
	}
	h := startRemoteHub(t)
	newAPI(t, h, auth.RoleAdmin) // the first admin
	creds := enrollNew(t, h, "db1")

	var file bytes.Buffer
	if _, err := backup.Create(context.Background(), backup.CreateOptions{Storage: h.cfg.Storage(), HubVersion: "9.9.9", Passphrase: "pass phrase", WorkFactor: 10}, &file); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	// A new machine: an empty data directory.
	cfg := h.cfg
	cfg.DataDir = t.TempDir()
	if _, err := backup.Restore(context.Background(), &file, backup.RestoreOptions{Storage: cfg.Storage(), Passphrase: "pass phrase"}); err != nil {
		t.Fatal(err)
	}
	h2 := startHubIn(t, cfg)
	waitFor(t, "the agent listener", func() bool { return h2.AgentAddr() != "" })

	// The user is there, with the password they had.
	if _, _, _, err := h2.auth.Login(context.Background(), "1.1.1.1", "admin", "correct horse battery"); err != nil {
		t.Errorf("the admin cannot sign in after the restore: %v", err)
	}
	// The agent connects with the credentials it already has.
	runAgentVersion(t, h2, creds, "9.9.9")
	waitFor(t, "the enrolled agent", func() bool {
		for _, a := range h2.Agents() {
			if a.ID == creds.AgentID {
				return true
			}
		}
		return false
	})
}
