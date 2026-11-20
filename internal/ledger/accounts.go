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

var ErrInvalidAccount = errors.New("Choose a saved account belonging to this bank.")
var ErrAccountDetails = errors.New("Enter a bank identifier (up to 64 bytes) and account name (up to 128 bytes).")

func ensureSchema(ctx context.Context, db *sql.DB) error {
	for _, ddl := range schema {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func Accounts(ctx context.Context, db *sql.DB) ([]Account, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT id,bank,label FROM accounts ORDER BY bank,label")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []Account{}
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.ID, &account.Bank, &account.Label); err != nil {
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
