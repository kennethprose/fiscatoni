package ledger

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"fiscatoni/internal/importer"
)

var ErrTransactionNotFound = errors.New("This transaction is no longer in the ledger.")
var ErrMissingMerchant = errors.New("This transaction has no merchant or description to remember.")

type CategoryEditOptions struct {
	AllForMerchant      bool `json:"allForMerchant"`
	RememberForMerchant bool `json:"rememberForMerchant"`
}

var ErrInvalidCategoryEdit = errors.New("Choose a category before remembering it for future imports.")

func UpdateTransactionCategory(ctx context.Context, db *sql.DB, id int64, category string, edit CategoryEditOptions) error {
	if edit.RememberForMerchant && category == "" {
		return ErrInvalidCategoryEdit
	}
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var selected importer.Row
	err = tx.QueryRowContext(ctx, "SELECT merchant,description FROM transactions WHERE id=?", id).
		Scan(&selected.Merchant, &selected.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTransactionNotFound
	}
	if err != nil {
		return err
	}
	identity := selected.MerchantIdentity()
	key := merchantKey(selected)
	ids := []int64{id}
	if edit.AllForMerchant && identity != "" {
		ids, err = matchingTransactionIDs(ctx, tx, key)
		if err != nil {
			return err
		}
	}

	if err := registerCategory(ctx, tx, category); err != nil {
		return err
	}
	if edit.RememberForMerchant {
		if identity == "" {
			return ErrMissingMerchant
		}
		// Imports also update rules before writing transaction rows.
		if err := saveMerchantCategory(ctx, tx, key, selected.MerchantName(), category); err != nil {
			return err
		}
	}

	if _, err := updateTransactionCategories(ctx, tx, ids, category); err != nil {
		return err
	}

	// A locking read checks the current row after any concurrent delete.
	var anchor int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM transactions WHERE id=? FOR UPDATE", id).Scan(&anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTransactionNotFound
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func updateTransactionCategories(ctx context.Context, tx *sql.Tx, ids []int64, category string) (int64, error) {
	// Acquire locks in ID order and keep each statement small.
	var updated int64
	for batch := range slices.Chunk(ids, 500) {
		args := make([]any, 1, len(batch)+1)
		args[0] = category
		for _, id := range batch {
			args = append(args, id)
		}
		query := "UPDATE transactions SET category=? WHERE id IN (?" + strings.Repeat(",?", len(batch)-1) + ") ORDER BY id"
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updated += changed
	}
	return updated, nil
}

func matchingTransactionIDs(ctx context.Context, tx *sql.Tx, key [32]byte) ([]int64, error) {
	// Normalize in Go so old imports and merchant rules use the same matching.
	rows, err := tx.QueryContext(ctx, "SELECT id,merchant,description FROM transactions ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		var row importer.Row
		if err := rows.Scan(&id, &row.Merchant, &row.Description); err != nil {
			return nil, err
		}
		if merchantKey(row) == key {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func DeleteTransaction(ctx context.Context, db *sql.DB, id int64) error {
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var accountID int64
	if err := tx.QueryRowContext(ctx, "SELECT account_id FROM transactions WHERE id=?", id).Scan(&accountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTransactionNotFound
		}
		return err
	}
	// Match the import path's lock order and read the row again under the lock.
	if err := tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE id=? FOR UPDATE", accountID).Scan(&accountID); err != nil {
		return err
	}
	var importID int64
	var line int
	err = tx.QueryRowContext(ctx, "SELECT import_id,source_line FROM transactions WHERE id=? FOR UPDATE", id).Scan(&importID, &line)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTransactionNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO deleted_import_rows(import_id,source_line) VALUES(?,?)", importID, line); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM transactions WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func rowsToReimport(ctx context.Context, tx *sql.Tx, importID int64, batch []importer.Row) ([]importer.Row, error) {
	rows, err := tx.QueryContext(ctx, "SELECT source_line FROM deleted_import_rows WHERE import_id=?", importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deleted := make(map[int]bool)
	for rows.Next() {
		var line int
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		deleted[line] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var retry []importer.Row
	for _, row := range batch {
		if deleted[row.Line] {
			retry = append(retry, row)
		}
	}
	return retry, nil
}
