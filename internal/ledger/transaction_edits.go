package ledger

import (
	"context"
	"database/sql"
	"errors"

	"fiscatoni/internal/importer"
)

var ErrTransactionNotFound = errors.New("This transaction is no longer in the ledger.")

func UpdateTransactionCategory(ctx context.Context, db *sql.DB, id int64, category string) error {
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, "UPDATE transactions SET category=? WHERE id=?", category, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed > 0 {
		return err
	}
	// MySQL reports zero affected rows if the requested category is already set.
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM transactions WHERE id=?)", id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrTransactionNotFound
	}
	return nil
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
