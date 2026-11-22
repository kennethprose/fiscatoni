package ledger

import (
	"context"
	"database/sql"
	"regexp"
	"slices"

	"fiscatoni/internal/importer"
)

type MerchantRuleMatch struct {
	Transaction
	Blocked          bool   `json:"blocked"`
	ExistingCategory string `json:"existingCategory,omitempty"`
}

type MerchantRulePreview struct {
	Rows     []MerchantRuleMatch `json:"rows"`
	Total    int                 `json:"total"`
	Eligible int                 `json:"eligible"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

func PreviewMerchantPattern(ctx context.Context, db *sql.DB, pattern string, page int, regexID int64) (MerchantRulePreview, error) {
	result := MerchantRulePreview{Rows: []MerchantRuleMatch{}, Page: page, PageSize: transactionsPerPage}
	compiled, err := compileMerchantPattern(pattern)
	if err != nil {
		return result, err
	}
	if err := ensureSchema(ctx, db); err != nil {
		return result, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if regexID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM merchant_regex_rules WHERE id=?)", regexID).Scan(&exists); err != nil {
			return result, err
		}
		if !exists {
			return result, ErrMerchantRuleNotFound
		}
	}
	existing, err := loadMerchantMatcher(ctx, tx, regexID)
	if err != nil {
		return result, err
	}
	start := int64(page-1) * int64(result.PageSize)
	err = scanMerchantPatternMatches(ctx, tx, compiled, existing, func(row MerchantRuleMatch) {
		index := int64(result.Total)
		if index >= start && index < start+int64(result.PageSize) {
			result.Rows = append(result.Rows, row)
		}
		result.Total++
		if !row.Blocked {
			result.Eligible++
		}
	})
	return result, err
}

func applyExactMerchantRule(ctx context.Context, tx *sql.Tx, key [32]byte, category string) (int64, error) {
	ids, err := matchingTransactionIDs(ctx, tx, key)
	if err != nil {
		return 0, err
	}
	return updateTransactionCategories(ctx, tx, ids, category)
}

func applyMerchantPattern(ctx context.Context, tx *sql.Tx, pattern *regexp.Regexp, regexID int64, category string) (int64, error) {
	// Exact matches and regex rules higher in the saved order take precedence.
	existing, err := loadMerchantMatcher(ctx, tx, regexID)
	if err != nil {
		return 0, err
	}
	var ids []int64
	err = scanMerchantPatternMatches(ctx, tx, pattern, existing, func(row MerchantRuleMatch) {
		if !row.Blocked {
			ids = append(ids, row.ID)
		}
	})
	if err != nil {
		return 0, err
	}
	slices.Sort(ids)
	return updateTransactionCategories(ctx, tx, ids, category)
}

func scanMerchantPatternMatches(ctx context.Context, tx *sql.Tx, pattern *regexp.Regexp, existing *merchantMatcher, visit func(MerchantRuleMatch)) error {
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.activity_date, t.description, t.merchant,
		a.id, a.bank, a.label, t.category, a.currency, t.amount_cents
		FROM transactions t JOIN accounts a ON t.account_id=a.id
		ORDER BY t.activity_date DESC, t.id DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row MerchantRuleMatch
		if err := rows.Scan(&row.ID, &row.Date, &row.Description, &row.Merchant, &row.Account.ID,
			&row.Account.Bank, &row.Account.Label, &row.Category, &row.Currency, &row.Cents); err != nil {
			return err
		}
		merchant := importer.Row{Merchant: row.Merchant, Description: row.Description}
		identity := merchant.MerchantIdentity()
		if pattern.MatchString(identity) {
			row.Merchant = merchant.MerchantName()
			row.ExistingCategory, row.Blocked = existing.categoryFor(identity)
			visit(row)
		}
	}
	return rows.Err()
}
