package ledger

import (
	"context"
	"database/sql"
	"fmt"
)

// Reset deletes the application's ledger data in one transaction.
func Reset(ctx context.Context, db *sql.DB) error {
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Imports lock their account before writing. Lock existing accounts and
	// the primary-key gaps so a new account can't start an import mid-reset.
	accounts, err := tx.QueryContext(ctx, "SELECT id FROM accounts FORCE INDEX (PRIMARY) FOR UPDATE")
	if err != nil {
		return err
	}
	for accounts.Next() {
		// Drain the locking query before issuing the deletes on this connection.
	}
	accounts.Close()
	if err := accounts.Err(); err != nil {
		return err
	}

	for _, table := range []string{"transactions", "deleted_import_rows", "imports", "merchant_categories", "accounts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return tx.Commit()
}
