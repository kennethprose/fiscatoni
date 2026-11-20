package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"fiscatoni/internal/importer"
)

// A description is the merchant identifier for exports without a merchant column.
func merchantKey(row importer.Row) [32]byte {
	name := row.Merchant
	if name == "" {
		name = row.Description
	}
	return sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(name), " "))))
}

type CategoryOptions struct {
	Names       []string       `json:"names"`
	Suggestions map[int]string `json:"suggestions"`
}

type ruleReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func merchantSuggestions(ctx context.Context, reader ruleReader, batch []importer.Row) (map[int]string, error) {
	rows, err := reader.QueryContext(ctx, "SELECT merchant_key,category FROM merchant_categories")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := map[[32]byte]string{}
	for rows.Next() {
		var key []byte
		var category string
		if err := rows.Scan(&key, &category); err != nil {
			return nil, err
		}
		rules[[32]byte(key)] = category
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	matches := map[int]string{}
	for _, row := range batch {
		if category, ok := rules[merchantKey(row)]; ok {
			matches[row.Line] = category
		}
	}
	return matches, nil
}

func CategoriesForReview(ctx context.Context, db *sql.DB, batch []importer.Row) (CategoryOptions, error) {
	var options CategoryOptions
	if err := ensureSchema(ctx, db); err != nil {
		return options, err
	}
	matches, err := merchantSuggestions(ctx, db, batch)
	if err != nil {
		return options, err
	}
	options.Suggestions = matches
	names, err := categoryNames(ctx, db)
	if err != nil {
		return options, err
	}
	options.Names = slices.DeleteFunc(names, func(name string) bool { return utf8.RuneCountInString(name) > 128 })
	return options, nil
}

func TransactionCategories(ctx context.Context, db *sql.DB) ([]string, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}
	return categoryNames(ctx, db)
}

func categoryNames(ctx context.Context, db *sql.DB) ([]string, error) {
	// The ledger itself is the category catalog, including older and CLI imports.
	rows, err := db.QueryContext(ctx, "SELECT DISTINCT CAST(category AS BINARY) FROM transactions WHERE category <> '' ORDER BY 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

var ErrMerchantConflict = errors.New("Choose one category to remember for each merchant. You can still give individual transactions different categories.")

func rememberCategories(ctx context.Context, tx *sql.Tx, kept []importer.Row, remember map[int]bool) error {
	written := map[[32]byte]string{}
	for _, row := range kept {
		if !remember[row.Line] {
			continue
		}
		key := merchantKey(row)
		if prev, ok := written[key]; ok {
			if prev != row.Category {
				return ErrMerchantConflict
			}
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO merchant_categories(merchant_key,category) VALUES(?,?)
			ON DUPLICATE KEY UPDATE category=?`, key[:], row.Category, row.Category)
		if err != nil {
			return err
		}
		written[key] = row.Category
	}
	return nil
}
