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

	if err := lockLedgerAccounts(ctx, tx); err != nil {
		return err
	}

	// Clear rules before transaction rows, matching the lock order of category edits.
	for _, table := range []string{"merchant_categories", "merchant_regex_rules", "transactions", "deleted_import_rows", "imports", "accounts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return tx.Commit()
}

func lockLedgerAccounts(ctx context.Context, tx *sql.Tx) error {
	// Imports lock their account before writing. Lock accounts and primary-key
	// gaps so a new account can't start an import during a ledger-wide change.
	accounts, err := tx.QueryContext(ctx, "SELECT id FROM accounts FORCE INDEX (PRIMARY) FOR UPDATE")
	if err != nil {
		return err
	}
	defer accounts.Close()
	for accounts.Next() {
		// Drain the locking query before using the transaction again.
	}
	return accounts.Err()
}
