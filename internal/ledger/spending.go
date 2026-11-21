package ledger

import (
	"context"
	"database/sql"
	"time"
)

type SpendGroup struct {
	Label string `json:"label"`
	Cents string `json:"cents"`
	Count int64  `json:"count"`
}

type AccountSpending struct {
	Account
	Cents string `json:"cents"`
}

type SpendingReport struct {
	End        string            `json:"end"`
	Months     []SpendGroup      `json:"months"`
	Income     []SpendGroup      `json:"income"`
	Categories []SpendGroup      `json:"categories"`
	Merchants  []SpendGroup      `json:"merchants"`
	Accounts   []AccountSpending `json:"accounts"`
}

const totalSpending = "SUM(-CAST(t.amount_cents AS DECIMAL(20,0)))"
const totalIncome = "SUM(CAST(t.amount_cents AS DECIMAL(20,0)))"

// Spending aggregates the same outgoing transactions available in the ledger's
// spending filter. Totals use DECIMAL and cross the JSON boundary as strings.
func Spending(ctx context.Context, db *sql.DB, accountID int64, end time.Time, months int) (SpendingReport, error) {
	report := SpendingReport{}
	if err := ensureSchema(ctx, db); err != nil {
		return report, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	filters := TransactionFilters{AccountID: accountID, Direction: "net"}
	const source = " FROM transactions t JOIN accounts a ON t.account_id=a.id"
	if end.IsZero() {
		where, args := transactionConditions(filters)
		var latest sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT DATE_FORMAT(MAX(t.activity_date),'%Y-%m')"+source+where, args...).Scan(&latest); err != nil {
			return report, err
		}
		if latest.Valid {
			end, err = time.Parse("2006-01", latest.String)
			if err != nil {
				return report, err
			}
		} else {
			now := time.Now().UTC()
			end = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	const monthLayout = "2006-01"
	report.End = end.Format(monthLayout)
	start := end.AddDate(0, 1-months, 0)
	if start.Year() < 1000 {
		start = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	filters.From = start.Format(time.DateOnly)
	filters.To = end.AddDate(0, 1, -1).Format(time.DateOnly)
	filters.Direction = "income"
	incomeWhere, incomeArgs := transactionConditions(filters)
	const monthGroup = "DATE_FORMAT(t.activity_date,'%Y-%m')"
	income, err := reportGroups(ctx, tx, monthGroup, totalIncome, source+incomeWhere, "1", incomeArgs)
	if err != nil {
		return report, err
	}
	report.Income = monthlyGroups(income, start, end)

	filters.Direction = "spend"
	where, args := transactionConditions(filters)
	monthly, err := reportGroups(ctx, tx, monthGroup, totalSpending, source+where, "1", args)
	if err != nil {
		return report, err
	}
	report.Months = monthlyGroups(monthly, start, end)
	report.Categories, err = reportGroups(ctx, tx, "CAST(t.category AS BINARY)", totalSpending, source+where, "2 DESC, 1", args)
	if err != nil {
		return report, err
	}
	report.Merchants, err = reportGroups(ctx, tx, "CAST("+merchantLabel+" AS BINARY)", totalSpending, source+where, "2 DESC, 1 LIMIT 6", args)
	if err != nil {
		return report, err
	}
	report.Accounts, err = spendingByAccount(ctx, tx, filters.From, filters.To)
	return report, err
}

func monthlyGroups(monthly []SpendGroup, start, end time.Time) []SpendGroup {
	byMonth := make(map[string]SpendGroup, len(monthly))
	for _, group := range monthly {
		byMonth[group.Label] = group
	}
	var groups []SpendGroup
	for month := start; !month.After(end); month = month.AddDate(0, 1, 0) {
		label := month.Format("2006-01")
		group, ok := byMonth[label]
		if !ok {
			group = SpendGroup{Label: label, Cents: "0"}
		}
		groups = append(groups, group)
	}
	return groups
}

func spendingByAccount(ctx context.Context, tx *sql.Tx, from, to string) ([]AccountSpending, error) {
	// Keep accounts with no spending in the comparison by filtering within the join.
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.bank, a.label, COALESCE(`+totalSpending+`, 0) AS total
		FROM accounts a LEFT JOIN transactions t ON t.account_id=a.id
			AND t.activity_date BETWEEN ? AND ? AND `+spendCondition+`
		WHERE a.currency='USD' GROUP BY a.id ORDER BY total DESC, a.label, a.bank, a.id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []AccountSpending{}
	for rows.Next() {
		var account AccountSpending
		if err := rows.Scan(&account.ID, &account.Bank, &account.Label, &account.Cents); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func reportGroups(ctx context.Context, tx *sql.Tx, group, total, source, order string, args []any) ([]SpendGroup, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+group+", "+total+", COUNT(*)"+
		source+" GROUP BY 1 ORDER BY "+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []SpendGroup{}
	for rows.Next() {
		var group SpendGroup
		if err := rows.Scan(&group.Label, &group.Cents, &group.Count); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}
