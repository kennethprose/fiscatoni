package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"unicode/utf8"

	"fiscatoni/internal/importer"
)

func merchantKey(row importer.Row) [32]byte {
	return sha256.Sum256([]byte(row.MerchantIdentity()))
}

type CategoryOptions struct {
	Names       []string       `json:"names"`
	Suggestions map[int]string `json:"suggestions"`
}

type ruleReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type patternRule struct {
	pattern  *regexp.Regexp
	category string
}

type merchantMatcher struct {
	exact    map[[32]byte]string
	patterns []patternRule
	cache    map[string]string
}

func loadMerchantMatcher(ctx context.Context, reader ruleReader, beforeRegexID int64) (*merchantMatcher, error) {
	rows, err := reader.QueryContext(ctx, `
		SELECT merchant_key, NULL AS pattern, category, 0 AS rule_order FROM merchant_categories
		UNION ALL
		SELECT NULL, r.pattern, r.category, `+regexPriority+` FROM `+regexRulesWithPriority+`
		WHERE ? = 0 OR `+regexPriority+` < COALESCE((SELECT priority FROM merchant_regex_priorities WHERE rule_id=?), ?)
		ORDER BY rule_order`, beforeRegexID, beforeRegexID, beforeRegexID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matcher := &merchantMatcher{exact: make(map[[32]byte]string), cache: make(map[string]string)}
	for rows.Next() {
		var key []byte
		var pattern sql.NullString
		var category string
		var order int64
		if err := rows.Scan(&key, &pattern, &category, &order); err != nil {
			return nil, err
		}
		if !pattern.Valid {
			matcher.exact[[32]byte(key)] = category
			continue
		}
		compiled, err := compileMerchantPattern(pattern.String)
		if err != nil {
			return nil, err
		}
		matcher.patterns = append(matcher.patterns, patternRule{compiled, category})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matcher, nil
}

func (m *merchantMatcher) categoryFor(identity string) (string, bool) {
	if category, ok := m.exact[sha256.Sum256([]byte(identity))]; ok {
		return category, true
	}
	if len(m.patterns) == 0 {
		return "", false
	}
	category, evaluated := m.cache[identity]
	if !evaluated {
		for _, rule := range m.patterns {
			if rule.pattern.MatchString(identity) {
				category = rule.category
				break
			}
		}
		m.cache[identity] = category
	}
	return category, category != ""
}

func merchantSuggestions(ctx context.Context, reader ruleReader, batch []importer.Row) (map[int]string, error) {
	matcher, err := loadMerchantMatcher(ctx, reader, 0)
	if err != nil {
		return nil, err
	}
	matches := map[int]string{}
	for _, row := range batch {
		if category, ok := matcher.categoryFor(row.MerchantIdentity()); ok {
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

func saveMerchantCategory(ctx context.Context, tx *sql.Tx, key [32]byte, name, category string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO merchant_categories(merchant_key,category) VALUES(?,?)
		ON DUPLICATE KEY UPDATE category=?`, key[:], category, category); err != nil {
		return err
	}
	return saveMerchantName(ctx, tx, key, name)
}

func saveMerchantName(ctx context.Context, tx *sql.Tx, key [32]byte, name string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO merchant_rule_names(merchant_key,merchant_name) VALUES(?,?)
		ON DUPLICATE KEY UPDATE merchant_name=?`, key[:], name, name)
	return err
}

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
		if err := saveMerchantCategory(ctx, tx, key, row.MerchantName(), row.Category); err != nil {
			return err
		}
		written[key] = row.Category
	}
	return nil
}
