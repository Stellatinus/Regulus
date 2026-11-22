package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// CreateFirstAdmin creates the first admin account straight in the database,
// for a server where no browser can reach the dashboard (`regulus hub
// create-admin`, and the console's first run). It follows the rules of
// RecoverAdmin: whoever can read and write the data directory owns the hub, so
// the one-time setup token is not asked, and the account is refused when the
// hub already has any account (ErrSetupDone). The password must pass the same
// checks as the dashboard's setup. It does not start a session.
//
// A hub that is running keeps its setup token in memory until it restarts;
// the token no longer works, because the setup form is refused as soon as an
// account exists.
func CreateFirstAdmin(ctx context.Context, db *sql.DB, username, password string, log *slog.Logger) (User, error) {
	s := &Service{store: store{db: db}, log: log, now: time.Now, failures: map[string]*failure{}}
	username = normalizeUsername(username)
	if err := validateCredentials(username, password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	user, err := s.store.createFirstAdmin(ctx, username, hash, s.now())
	if err != nil {
		return User{}, err
	}
	s.log.Info("admin account created", "user", user.Username)
	s.audit(ctx, ConsoleActor, EventSetup, user.Username, "first admin (create-admin command)")
	return user, nil
}
