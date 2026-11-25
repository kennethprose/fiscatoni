package ledger

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"unicode/utf8"

	"fiscatoni/internal/importer"
)

type MerchantRule struct {
	ID       string `json:"id"`
	Merchant string `json:"merchant"`
	Category string `json:"category"`
	Pattern  string `json:"pattern,omitempty"`
}

var ErrMerchantRuleNotFound = errors.New("This merchant rule no longer exists.")
var ErrDuplicateMerchantRule = errors.New("A rule already exists for this merchant or pattern. Edit that rule to change its category.")
var ErrInvalidMerchantPattern = errors.New("Enter a valid Go regular expression of up to 512 characters.")
var ErrMerchantRuleOrderChanged = errors.New("The regex rule order changed. Try moving the rule again.")

// Rules without a priority override keep their original creation order.
const regexPriority = "COALESCE(p.priority,r.id)"
const regexRulesWithPriority = "merchant_regex_rules r LEFT JOIN merchant_regex_priorities p ON p.rule_id=r.id"

func compileMerchantPattern(pattern string) (*regexp.Regexp, error) {
	if pattern == "" || utf8.RuneCountInString(pattern) > 512 {
		return nil, ErrInvalidMerchantPattern
	}
	compiled, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, ErrInvalidMerchantPattern
	}
	return compiled, nil
}

func duplicateRuleError(err error) error {
	if isDuplicateKey(err) {
		return ErrDuplicateMerchantRule
	}
	return err
}

func CreateMerchantRule(ctx context.Context, db *sql.DB, match, category string, regex, applyToHistory bool) (int64, error) {
	var pattern *regexp.Regexp
	if regex {
		var err error
		pattern, err = compileMerchantPattern(match)
		if err != nil {
			return 0, err
		}
	}
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := registerCategory(ctx, tx, category); err != nil {
		return 0, err
	}

	var updated int64
	if regex {
		result, insertErr := tx.ExecContext(ctx, "INSERT INTO merchant_regex_rules(pattern,category) VALUES(?,?)", match, category)
		if insertErr != nil {
			return 0, duplicateRuleError(insertErr)
		}
		if applyToHistory {
			id, idErr := result.LastInsertId()
			if idErr != nil {
				return 0, idErr
			}
			updated, err = applyMerchantPattern(ctx, tx, pattern, id, category)
		}
	} else {
		key := merchantKey(importer.Row{Merchant: match})
		if _, err := tx.ExecContext(ctx, "INSERT INTO merchant_categories(merchant_key,category) VALUES(?,?)", key[:], category); err != nil {
			return 0, duplicateRuleError(err)
		}
		if err := saveMerchantName(ctx, tx, key, match); err != nil {
			return 0, err
		}
		if applyToHistory {
			updated, err = applyExactMerchantRule(ctx, tx, key, category)
		}
	}
	return commitMerchantRuleChange(tx, updated, err)
}

func commitMerchantRuleChange(tx *sql.Tx, updated int64, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func MerchantRules(ctx context.Context, db *sql.DB) ([]MerchantRule, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT r.merchant_key, COALESCE(n.merchant_name,''), r.category
		FROM merchant_categories r LEFT JOIN merchant_rule_names n USING (merchant_key)
		ORDER BY r.merchant_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []MerchantRule{}
	unknown := make(map[[32]byte]int)
	for rows.Next() {
		var key []byte
		var rule MerchantRule
		if err := rows.Scan(&key, &rule.Merchant, &rule.Category); err != nil {
			return nil, err
		}
		rule.ID = hex.EncodeToString(key)
		if rule.Merchant == "" {
			unknown[[32]byte(key)] = len(rules)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		if err := recoverMerchantNames(ctx, tx, rules, unknown); err != nil {
			return nil, err
		}
	}
	patterns, err := tx.QueryContext(ctx, "SELECT r.id,r.pattern,r.category FROM "+regexRulesWithPriority+" ORDER BY "+regexPriority)
	if err != nil {
		return nil, err
	}
	defer patterns.Close()
	for patterns.Next() {
		var id int64
		var rule MerchantRule
		if err := patterns.Scan(&id, &rule.Pattern, &rule.Category); err != nil {
			return nil, err
		}
		rule.ID = "regex-" + strconv.FormatInt(id, 10)
		rules = append(rules, rule)
	}
	return rules, patterns.Err()
}

// SwapRegexRuleOrder moves two adjacent rules if they are still in the order shown to the user.
func SwapRegexRuleOrder(ctx context.Context, db *sql.DB, firstID, secondID int64) error {
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	firstPriority, secondPriority, err := adjacentRegexPriorities(ctx, tx, firstID, secondID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO merchant_regex_priorities(rule_id,priority)
		VALUES (?,?),(?,?) AS updated ON DUPLICATE KEY UPDATE priority=updated.priority`,
		firstID, secondPriority, secondID, firstPriority); err != nil {
		return err
	}
	return tx.Commit()
}

func adjacentRegexPriorities(ctx context.Context, tx *sql.Tx, firstID, secondID int64) (int64, int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT r.id,"+regexPriority+" AS priority FROM "+regexRulesWithPriority+" ORDER BY priority FOR UPDATE")
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	var previousID, previousPriority int64
	for rows.Next() {
		var id, priority int64
		if err := rows.Scan(&id, &priority); err != nil {
			return 0, 0, err
		}
		if previousID == firstID && id == secondID {
			return previousPriority, priority, rows.Close()
		}
		previousID, previousPriority = id, priority
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, ErrMerchantRuleOrderChanged
}

func recoverMerchantNames(ctx context.Context, tx *sql.Tx, rules []MerchantRule, unknown map[[32]byte]int) error {
	// Older rules stored only a hash. Match it against the latest saved rows.
	rows, err := tx.QueryContext(ctx, "SELECT merchant,description FROM transactions ORDER BY id DESC")
	if err != nil {
		return err
	}
	defer rows.Close()
	for len(unknown) > 0 && rows.Next() {
		var row importer.Row
		if err := rows.Scan(&row.Merchant, &row.Description); err != nil {
			return err
		}
		key := merchantKey(row)
		if index, ok := unknown[key]; ok {
			rules[index].Merchant = row.MerchantName()
			delete(unknown, key)
		}
	}
	return rows.Err()
}

func UpdateMerchantRule(ctx context.Context, db *sql.DB, id [32]byte, category string, applyToHistory bool) (int64, error) {
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := registerCategory(ctx, tx, category); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE merchant_categories SET category=? WHERE merchant_key=?", category, id[:])
	if err := checkRuleUpdate(ctx, tx, result, err, "SELECT EXISTS(SELECT 1 FROM merchant_categories WHERE merchant_key=?)", id[:]); err != nil {
		return 0, err
	}
	var updated int64
	if applyToHistory {
		updated, err = applyExactMerchantRule(ctx, tx, id, category)
	}
	return commitMerchantRuleChange(tx, updated, err)
}

func UpdateRegexMerchantRule(ctx context.Context, db *sql.DB, id int64, pattern *string, category string, applyToHistory bool) (int64, error) {
	var compiled *regexp.Regexp
	var err error
	if pattern != nil {
		compiled, err = compileMerchantPattern(*pattern)
		if err != nil {
			return 0, err
		}
	}
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := registerCategory(ctx, tx, category); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE merchant_regex_rules SET pattern=COALESCE(?,pattern), category=? WHERE id=?", pattern, category, id)
	if err := checkRuleUpdate(ctx, tx, result, duplicateRuleError(err), "SELECT EXISTS(SELECT 1 FROM merchant_regex_rules WHERE id=?)", id); err != nil {
		return 0, err
	}
	var updated int64
	if applyToHistory {
		if compiled == nil {
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT pattern FROM merchant_regex_rules WHERE id=?", id).Scan(&stored); err != nil {
				return 0, err
			}
			compiled, err = compileMerchantPattern(stored)
			if err != nil {
				return 0, err
			}
		}
		updated, err = applyMerchantPattern(ctx, tx, compiled, id, category)
	}
	return commitMerchantRuleChange(tx, updated, err)
}

func checkRuleUpdate(ctx context.Context, tx *sql.Tx, result sql.Result, err error, existsQuery string, id any) error {
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed > 0 {
		return err
	}
	// An unchanged category also returns zero affected rows in MySQL.
	var exists bool
	if err := tx.QueryRowContext(ctx, existsQuery, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrMerchantRuleNotFound
	}
	return nil
}

func DeleteMerchantRule(ctx context.Context, db *sql.DB, id [32]byte) error {
	return deleteMerchantRule(ctx, db, "DELETE FROM merchant_categories WHERE merchant_key=?", id[:])
}

func DeleteRegexMerchantRule(ctx context.Context, db *sql.DB, id int64) error {
	return deleteMerchantRule(ctx, db, "DELETE FROM merchant_regex_rules WHERE id=?", id)
}

func deleteMerchantRule(ctx context.Context, db *sql.DB, query string, id any) error {
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrMerchantRuleNotFound
	}
	return nil
}
