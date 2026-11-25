package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type Account struct {
	ID    int64  `json:"id,string"`
	Bank  string `json:"bank"`
	Label string `json:"label"`
}

type AccountUsage struct {
	Account
	HasTransactions bool `json:"hasTransactions"`
}

var ErrInvalidAccount = errors.New("Choose a saved account belonging to this bank.")
var ErrAccountDetails = errors.New("Enter a bank identifier (up to 64 bytes) and account name (up to 128 bytes).")
var ErrAccountNotFound = errors.New("This account no longer exists.")
var ErrAccountInUse = errors.New("Delete the account's transactions before deleting the account.")

func ensureSchema(ctx context.Context, db *sql.DB) error {
	for _, ddl := range schema {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return migrateCategories(ctx, db)
}

func Accounts(ctx context.Context, db *sql.DB) ([]AccountUsage, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.bank,a.label,
		EXISTS(SELECT 1 FROM transactions t WHERE t.account_id=a.id)
		FROM accounts a ORDER BY a.bank,a.label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []AccountUsage{}
	for rows.Next() {
		var account AccountUsage
		if err := rows.Scan(&account.ID, &account.Bank, &account.Label, &account.HasTransactions); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

const upsertAccount = `INSERT INTO accounts(bank,label) VALUES(?,?)
	ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`

func AddAccount(ctx context.Context, db *sql.DB, bank, label string) (Account, error) {
	account := Account{Bank: strings.TrimSpace(bank), Label: strings.TrimSpace(label)}
	if account.Bank == "" || len(account.Bank) > 64 || account.Label == "" || len(account.Label) > 128 {
		return Account{}, ErrAccountDetails
	}
	if err := ensureSchema(ctx, db); err != nil {
		return Account{}, err
	}
	result, err := db.ExecContext(ctx, upsertAccount, account.Bank, account.Label)
	if err != nil {
		return Account{}, err
	}
	account.ID, err = result.LastInsertId()
	return account, err
}

func DeleteAccount(ctx context.Context, db *sql.DB, id int64) error {
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Imports lock the destination account before writing. Check its current
	// transactions while holding the same lock.
	err = tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE id=? FOR UPDATE", id).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM transactions WHERE account_id=?)", id).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrAccountInUse
	}

	// An empty account can still have import records and deleted CSV lines.
	if _, err := tx.ExecContext(ctx, `DELETE d FROM deleted_import_rows d
		JOIN imports i ON i.id=d.import_id WHERE i.account_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM imports WHERE account_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM accounts WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
