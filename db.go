package mup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const dbName = "mup.db"

func OpenDB(dirpath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath.Join(dirpath, dbName)+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1000)

	err = updateSchema(db)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func WipeDB(dirpath string) error {
	err1 := os.Remove(filepath.Join(dirpath, dbName))
	err2 := os.Remove(filepath.Join(dirpath, dbName+"-wal"))
	err3 := os.Remove(filepath.Join(dirpath, dbName+"-shm"))
	for _, err := range []error{err1, err2, err3} {
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

const placersTemplate = "?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?"

func placers(columns string) string {
	if len(columns) == 0 {
		return ""
	}
	return placersTemplate[:1+strings.Count(columns, ",")*2]
}

func updateSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query("SELECT 1 FROM sqlite_master WHERE type='table' AND name='option'")
	if err != nil {
		return err
	}
	var major, minor int
	if rows.Next() {
		rows, err = tx.Query("SELECT (SELECT value FROM option WHERE name='schema_major'), (SELECT value FROM option WHERE name='schema_minor')")
		if err != nil {
			return err
		}
		if !rows.Next() || rows.Scan(&major, &minor) != nil {
			return fmt.Errorf("mup database lacks schema_major and schema_minor")
		}
	}

	for _, patch := range schemaPatches {
		if patch.major < major || patch.major == major && patch.minor <= minor {
			continue
		}
		if patch.major > major+1 || patch.major == major+1 && patch.minor > 0 {
			return fmt.Errorf("cannot update database schema version from %d.%d to %d.%d", major, minor, patch.major, patch.minor)
		}
		err := patch.apply(tx)
		if err != nil {
			return fmt.Errorf("cannot update database schema version from %d.%d to %d.%d: %v", major, minor, patch.major, patch.minor, err)
		}
		major, minor = patch.major, patch.minor
	}

	_, err = tx.Exec("UPDATE option SET value=? WHERE name='schema_major'; UPDATE option SET value=? WHERE name='schema_minor'", major, minor)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// FIXME Invert the patch logic so that the latest one is the current schema.

var schemaPatches = []struct {
	major int
	minor int
	apply func(*sql.Tx) error
}{
	{1, 0, schemaPatch1_0},
}

func schemaPatch1_0(tx *sql.Tx) error {
	// As a general rule for table schemas below, the behavior of inserting a row
	// with default values must have the same effect as inserting a zero Go value.
	// That means, for example, that the default for all text rows is "", as that's
	// the default value of a Go string.
	var stmts = []string{
		"ROLLBACK",
		"PRAGMA journal_mode=WAL",
		"PRAGMA auto_vacuum=INCREMENTAL",
		"BEGIN",
		"CREATE TABLE option (name TEXT NOT NULL, value)",
		"INSERT INTO option VALUES ('schema_major', NULL)",
		"INSERT INTO option VALUES ('schema_minor', NULL)",
		"CREATE TABLE account (" +
			// FIXME Add a space before every column name.
			// FIXME Remove all underlines from column names.
			"name TEXT NOT NULL PRIMARY KEY," +
			"kind TEXT NOT NULL DEFAULT ''," +
			"endpoint TEXT NOT NULL DEFAULT ''," +
			"host TEXT NOT NULL DEFAULT ''," +
			"tls BOOLEAN NOT NULL DEFAULT FALSE," +
			"tls_insecure BOOLEAN NOT NULL DEFAULT FALSE," +
			"nick TEXT NOT NULL DEFAULT ''," +
			"identity TEXT NOT NULL DEFAULT ''," +
			"password TEXT NOT NULL DEFAULT ''," +
			"last_id INTEGER NOT NULL DEFAULT 0)",
		"CREATE TABLE channel (" +
			"account TEXT NOT NULL REFERENCES account(name) ON UPDATE CASCADE ON DELETE CASCADE," +
			"name TEXT NOT NULL DEFAULT ''," +
			"key TEXT NOT NULL DEFAULT ''," +
			"PRIMARY KEY (account,name))",
		"CREATE TABLE message (" +
			"id INTEGER PRIMARY KEY AUTOINCREMENT," +
			"nonce BLOB NOT NULL DEFAULT (hex(randomblob(16)))," +
			"lane INTEGER NOT NULL DEFAULT 0," +
			"time DATETIME NOT NULL DEFAULT 0," +
			"account TEXT NOT NULL DEFAULT ''," +
			"channel TEXT NOT NULL DEFAULT ''," +
			"nick TEXT NOT NULL DEFAULT ''," +
			"user TEXT NOT NULL DEFAULT ''," +
			"host TEXT NOT NULL DEFAULT ''," +
			"command TEXT NOT NULL DEFAULT ''," +
			"params TEXT NOT NULL DEFAULT ''," +
			"text TEXT NOT NULL DEFAULT ''," +
			"bot_text TEXT NOT NULL DEFAULT ''," +
			"bang TEXT NOT NULL DEFAULT ''," +
			"as_nick TEXT NOT NULL DEFAULT ''," +
			"UNIQUE (nonce,lane))",
		"CREATE TABLE log (" +
			"id INTEGER PRIMARY KEY DEFAULT 0," +
			"nonce BLOB NOT NULL DEFAULT ''," +
			"lane INTEGER NOT NULL DEFAULT 0," +
			"time DATETIME NOT NULL DEFAULT 0," +
			"account TEXT NOT NULL DEFAULT ''," +
			"channel TEXT NOT NULL DEFAULT ''," +
			"nick TEXT NOT NULL DEFAULT ''," +
			"user TEXT NOT NULL DEFAULT ''," +
			"host TEXT NOT NULL DEFAULT ''," +
			"command TEXT NOT NULL DEFAULT ''," +
			"params TEXT NOT NULL DEFAULT ''," +
			"text TEXT NOT NULL DEFAULT ''," +
			"bot_text TEXT NOT NULL DEFAULT ''," +
			"bang TEXT NOT NULL DEFAULT ''," +
			"as_nick TEXT NOT NULL DEFAULT ''," +
			"UNIQUE (nonce,lane))",
		"CREATE TABLE plugin (" +
			"name TEXT NOT NULL PRIMARY KEY," +
			"last_id INTEGER NOT NULL DEFAULT 0," +
			"config TEXT NOT NULL DEFAULT ''," +
			"state TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE target (" +
			"plugin TEXT NOT NULL REFERENCES plugin(name) ON UPDATE CASCADE ON DELETE CASCADE," +
			"account TEXT NOT NULL REFERENCES account(name) ON UPDATE CASCADE ON DELETE CASCADE," +
			"channel TEXT NOT NULL DEFAULT ''," +
			"nick TEXT NOT NULL DEFAULT ''," +
			"config TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE moniker (" +
			"account TEXT NOT NULL REFERENCES account(name) ON UPDATE CASCADE ON DELETE CASCADE," +
			"channel TEXT NOT NULL DEFAULT ''," +
			"nick TEXT NOT NULL DEFAULT ''," +
			"name TEXT NOT NULL DEFAULT ''," +
			"PRIMARY KEY (account,channel,nick))",
		"CREATE TABLE ldap (" +
			"name TEXT NOT NULL PRIMARY KEY," +
			"url TEXT NOT NULL DEFAULT ''," +
			"base_dn TEXT NOT NULL DEFAULT ''," +
			"bind_dn TEXT NOT NULL DEFAULT ''," +
			"bind_pass TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE plugin_schema (" +
			"plugin TEXT NOT NULL PRIMARY KEY," +
			"help TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE command_schema (" +
			"plugin TEXT NOT NULL REFERENCES plugin_schema(plugin) ON UPDATE CASCADE ON DELETE CASCADE," +
			"command TEXT NOT NULL," +
			"help TEXT NOT NULL DEFAULT ''," +
			"hide BOOLEAN NOT NULL DEFAULT false," +
			"PRIMARY KEY (plugin,command))",
		"CREATE TABLE argument_schema (" +
			"plugin TEXT NOT NULL," +
			"command TEXT NOT NULL," +
			"argument TEXT NOT NULL," +
			"hint TEXT NOT NULL DEFAULT ''," +
			"type TEXT NOT NULL DEFAULT ''," +
			"flag INTEGER NOT NULL DEFAULT 0," +
			"FOREIGN KEY (plugin,command) REFERENCES command_schema(plugin,command) ON UPDATE CASCADE ON DELETE CASCADE," +
			"PRIMARY KEY (plugin,command,argument))",
		"CREATE TABLE user (" +
			"account TEXT NOT NULL REFERENCES account(name) ON UPDATE CASCADE ON DELETE CASCADE," +
			"nick TEXT NOT NULL," +
			"password_hash TEXT NOT NULL DEFAULT ''," +
			"password_salt TEXT NOT NULL DEFAULT ''," +
			"attempt_start DATETIME NOT NULL DEFAULT 0," +
			"attempt_count INTEGER NOT NULL DEFAULT 0," +
			"admin BOOLEAN NOT NULL DEFAULT FALSE," +
			"PRIMARY KEY (account,nick))",
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
