// Package backup makes and restores the encrypted backup file of a hub
// (docs/backup.md, #253): one tar stream, encrypted with a passphrase by age
// (filippo.io/age), holding a manifest, the portable export of the database
// (one JSON-lines file per table, the same for SQLite and PostgreSQL), the
// secret key and the CA directory.
//
// Create writes a backup while the hub runs; Extract decrypts a backup and
// checks every checksum without touching anything of the installation;
// Restore puts one into an empty data directory and database, migrating it
// forward. It does not schedule backups, send them anywhere, serve them over
// HTTP or ask for a passphrase: the console command (cmd/regulus) and the
// later dashboard steps do. It does not back up the configuration files (root
// owns them) and it does not know SQL: reading and writing the tables is
// internal/storage.
package backup
