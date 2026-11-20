package ledger

import (
	"context"
	"database/sql"
	"strings"
)

const transactionsPerPage = 25

type TransactionFilters struct {
	AccountID int64
	Category  *string // An empty string selects uncategorized transactions.
	From      string
	To        string
	Search    string
	Direction string
	Page      int
}

type Transaction struct {
	ID          int64   `json:"id,string"`
	Date        string  `json:"date"`
	Description string  `json:"description"`
	Merchant    string  `json:"merchant"`
	Account     Account `json:"account"`
	Category    string  `json:"category"`
	Currency    string  `json:"currency"`
	Cents       int64   `json:"cents,string"`
}

type TransactionPage struct {
	Rows     []Transaction `json:"rows"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
}

func Transactions(ctx context.Context, db *sql.DB, filters TransactionFilters) (TransactionPage, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return TransactionPage{}, err
	}
	// Count and page must describe the same ledger snapshot during an import.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return TransactionPage{}, err
	}
	defer tx.Rollback()

	where, args := transactionConditions(filters)
	source := " FROM transactions t JOIN accounts a ON t.account_id=a.id" + where
	result := TransactionPage{Rows: []Transaction{}, PageSize: transactionsPerPage}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+source, args...).Scan(&result.Total); err != nil {
		return TransactionPage{}, err
	}
	lastPage := max(1, (result.Total+result.PageSize-1)/result.PageSize)
	result.Page = min(max(1, filters.Page), lastPage)
	args = append(args, result.PageSize, (result.Page-1)*result.PageSize)
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.activity_date, t.description, t.merchant,
		a.id, a.bank, a.label, t.category, a.currency, t.amount_cents`+source+
		" ORDER BY t.activity_date DESC, t.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return TransactionPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var row Transaction
		if err := rows.Scan(&row.ID, &row.Date, &row.Description, &row.Merchant, &row.Account.ID,
			&row.Account.Bank, &row.Account.Label, &row.Category, &row.Currency, &row.Cents); err != nil {
			return TransactionPage{}, err
		}
		result.Rows = append(result.Rows, row)
	}
	return result, rows.Err()
}

func transactionConditions(filters TransactionFilters) (string, []any) {
	var conditions []string
	var args []any
	if filters.AccountID > 0 {
		conditions = append(conditions, "t.account_id = ?")
		args = append(args, filters.AccountID)
	}
	if filters.Category != nil {
		conditions = append(conditions, "CAST(t.category AS BINARY) = CAST(? AS BINARY)")
		args = append(args, *filters.Category)
	}
	if filters.From != "" {
		conditions = append(conditions, "t.activity_date >= ?")
		args = append(args, filters.From)
	}
	if filters.To != "" {
		conditions = append(conditions, "t.activity_date <= ?")
		args = append(args, filters.To)
	}
	if filters.Search != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(filters.Search) + "%"
		conditions = append(conditions, `(t.description COLLATE utf8mb4_0900_ai_ci LIKE ? ESCAPE '!'
			OR t.merchant COLLATE utf8mb4_0900_ai_ci LIKE ? ESCAPE '!')`)
		args = append(args, pattern, pattern)
	}
	switch filters.Direction {
	case "out":
		conditions = append(conditions, "t.amount_cents < 0")
	case "in":
		conditions = append(conditions, "t.amount_cents > 0")
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}
