package tsdb

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dgraph-io/badger/v4"
)

// Snapshot writes a consistent backup of the database to w while it keeps
// serving reads and writes.
func (db *DB) Snapshot(w io.Writer) error {
	_, err := db.kv.Backup(w, 0)
	return err
}

// Restore loads a snapshot into dir, which must be empty or not exist, so a
// restore can never mix two databases.
func Restore(dir string, r io.Reader) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty; restore into a new directory", dir)
	}
	kv, err := badger.Open(badger.DefaultOptions(dir).WithLogger(nil))
	if err != nil {
		return err
	}
	if err := kv.Load(r, 256); err != nil {
		kv.Close()
		return fmt.Errorf("loading snapshot: %w", err)
	}
	return kv.Close()
}
