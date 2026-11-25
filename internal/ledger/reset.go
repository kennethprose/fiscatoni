package ledger

import (
	"context"
	"database/sql"
	"fmt"
)

// Reset deletes the application's ledger data in one transaction.
func Reset(ctx context.Context, db *sql.DB) error {
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Lock the catalog before rules and transactions, matching the write paths.
	for _, table := range []string{"categories", "merchant_categories", "merchant_regex_rules", "transactions", "deleted_import_rows", "imports", "accounts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return tx.Commit()
}

func beginLedgerWideChange(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	if err := lockLedgerAccounts(ctx, tx); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
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
