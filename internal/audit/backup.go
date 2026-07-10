package audit

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Backup creates a transactionally consistent SQLite snapshot and atomically
// publishes it at destination. The live Store remains usable while VACUUM
// INTO is running.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if s == nil || s.writeDB == nil {
		return fmt.Errorf("audit store is not open")
	}
	if destination == "" {
		return fmt.Errorf("backup destination is empty")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".audit-backup-*.sqlite")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpPath)
	defer os.Remove(tmpPath)

	s.mu.Lock()
	_, err = s.writeDB.ExecContext(ctx, `VACUUM INTO ?`, tmpPath)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("sqlite backup: %w", err)
	}
	if err := ValidateBackup(tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return fmt.Errorf("publish sqlite backup: %w", err)
	}
	return nil
}

// Restore validates source and copies it atomically to destination. It never
// mutates an already-open Store; callers should open a new Store afterwards.
func Restore(source, destination string) error {
	if err := ValidateBackup(source); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".audit-restore-*.sqlite")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ValidateBackup(tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}

func ValidateBackup(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite integrity check failed: %s", result)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("backup schema version %d, want %d", version, currentSchemaVersion)
	}
	return nil
}
