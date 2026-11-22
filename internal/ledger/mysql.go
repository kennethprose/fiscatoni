// Package ledger writes validated imports to MySQL.
package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"fiscatoni/internal/importer"
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS merchant_categories (
		merchant_key BINARY(32) PRIMARY KEY, category TEXT NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS merchant_rule_names (
		merchant_key BINARY(32) PRIMARY KEY, merchant_name MEDIUMTEXT NOT NULL,
		FOREIGN KEY(merchant_key) REFERENCES merchant_categories(merchant_key) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS merchant_regex_rules (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, pattern VARCHAR(512) NOT NULL, category TEXT NOT NULL,
		UNIQUE(pattern)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS merchant_regex_priorities (
		rule_id BIGINT PRIMARY KEY, priority BIGINT NOT NULL,
		FOREIGN KEY(rule_id) REFERENCES merchant_regex_rules(id) ON DELETE CASCADE
	) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS accounts (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, bank VARCHAR(64) NOT NULL, label VARCHAR(128) NOT NULL,
		currency CHAR(3) NOT NULL DEFAULT 'USD', UNIQUE(bank,label)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS imports (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, account_id BIGINT NOT NULL, file_hash BINARY(32) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(account_id,file_hash), UNIQUE(account_id,id), FOREIGN KEY(account_id) REFERENCES accounts(id)
	) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS deleted_import_rows (
		import_id BIGINT NOT NULL, source_line INT NOT NULL,
		PRIMARY KEY(import_id, source_line), FOREIGN KEY(import_id) REFERENCES imports(id)
	) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS transactions (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, account_id BIGINT NOT NULL, import_id BIGINT NOT NULL,
		source_line INT NOT NULL, activity_date DATE NOT NULL, posted_date DATE,
		description MEDIUMTEXT NOT NULL, merchant TEXT NOT NULL, category TEXT NOT NULL,
		source_type TEXT NOT NULL, source_status TEXT NOT NULL, source_time TEXT NOT NULL,
		memo MEDIUMTEXT NOT NULL, card_reference TEXT NOT NULL, amount_cents BIGINT NOT NULL,
		match_key BINARY(32) NOT NULL, INDEX(account_id,match_key), INDEX(account_id,activity_date),
		UNIQUE(import_id,source_line), FOREIGN KEY(account_id,import_id) REFERENCES imports(account_id,id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
}

type Result struct {
	ID              int64
	Added, Skipped  int
	AlreadyImported bool
}

type OverlapError struct{ Lines []int }

func (e *OverlapError) Error() string {
	lines := make([]string, len(e.Lines))
	for i, line := range e.Lines {
		lines[i] = strconv.Itoa(line)
	}
	return fmt.Sprintf("possible overlap on CSV lines %s; review with preview -rows, then use -keep-lines or -skip-lines; no rows saved", strings.Join(lines, ","))
}

// A match is a reason to review, never authority to discard a transaction.
func matchKey(r importer.Row) [32]byte {
	text := strings.ToLower(strings.Join(strings.Fields(r.Description), " "))
	return sha256.Sum256([]byte(r.Date + "|" + strconv.FormatInt(r.Cents, 10) + "|" + text))
}

// Save accepts explicit keep (true) or skip (false) decisions for potential overlaps.
func Save(ctx context.Context, db *sql.DB, account string, batch importer.Batch, decisions map[int]bool) (Result, error) {
	if strings.TrimSpace(account) == "" || len(account) > 128 {
		return Result{}, errors.New("account label must be 1 to 128 bytes")
	}
	return save(ctx, db, Account{Label: account}, batch, decisions, nil)
}

func SaveToAccount(ctx context.Context, db *sql.DB, id int64, batch importer.Batch, decisions, remember map[int]bool) (Result, error) {
	if id <= 0 {
		return Result{}, ErrInvalidAccount
	}
	return save(ctx, db, Account{ID: id}, batch, decisions, remember)
}

func save(ctx context.Context, db *sql.DB, account Account, batch importer.Batch, decisions, remember map[int]bool) (Result, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return Result{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	// Both entry points lock the account before checking existing imports.
	accountID := account.ID
	if accountID == 0 {
		res, err := tx.ExecContext(ctx, upsertAccount, batch.Bank, account.Label)
		if err != nil {
			return Result{}, err
		}
		accountID, err = res.LastInsertId()
		if err != nil {
			return Result{}, err
		}
	} else {
		err = tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE id=? AND bank=? FOR UPDATE", account.ID, batch.Bank).Scan(&accountID)
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, ErrInvalidAccount
		}
	}
	if err != nil {
		return Result{}, err
	}
	var result Result
	err = tx.QueryRowContext(ctx, "SELECT id FROM imports WHERE account_id=? AND file_hash=?", accountID, batch.Digest[:]).Scan(&result.ID)
	if err == nil {
		batch.Rows, err = rowsToReimport(ctx, tx, result.ID, batch.Rows)
		if err != nil {
			return Result{}, err
		}
		if len(batch.Rows) == 0 {
			result.AlreadyImported = true
			return result, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}

	// Check only previously committed rows, so matching purchases in one file survive.
	var keep []importer.Row
	var unresolved []int
	used := 0
	for _, r := range batch.Rows {
		k := matchKey(r)
		var exists bool
		err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM transactions WHERE account_id=? AND match_key=?)", accountID, k[:]).Scan(&exists)
		if err != nil {
			return Result{}, err
		}
		if exists {
			accept, ok := decisions[r.Line]
			if !ok {
				unresolved = append(unresolved, r.Line)
				continue
			}
			used++
			if !accept {
				result.Skipped++
				continue
			}
		}
		keep = append(keep, r)
	}
	if len(unresolved) > 0 {
		return Result{}, &OverlapError{Lines: unresolved}
	}
	if used != len(decisions) {
		return Result{}, errors.New("line decision does not match an overlapping row; no rows saved")
	}
	// Browser rows carry the reviewed values. CLI imports resolve rules here.
	if account.ID == 0 {
		matches, err := merchantSuggestions(ctx, tx, keep)
		if err != nil {
			return Result{}, err
		}
		for i, row := range keep {
			if category, ok := matches[row.Line]; ok {
				keep[i].Category = category
			}
		}
	}
	if err := rememberCategories(ctx, tx, keep, remember); err != nil {
		return Result{}, err
	}
	if result.ID == 0 {
		res, err := tx.ExecContext(ctx, "INSERT INTO imports(account_id,file_hash) VALUES(?,?)", accountID, batch.Digest[:])
		if err != nil {
			return Result{}, err
		}
		result.ID, err = res.LastInsertId()
		if err != nil {
			return Result{}, err
		}
	} else {
		// Resolve only the rows that were reviewed for this retry, including explicit skips.
		for _, row := range batch.Rows {
			if _, err := tx.ExecContext(ctx, "DELETE FROM deleted_import_rows WHERE import_id=? AND source_line=?", result.ID, row.Line); err != nil {
				return Result{}, err
			}
		}
	}
	for _, r := range keep {
		k := matchKey(r)
		_, err = tx.ExecContext(ctx, `INSERT INTO transactions(account_id,import_id,source_line,activity_date,posted_date,
			description,merchant,category,source_type,source_status,source_time,memo,card_reference,amount_cents,match_key)
			VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?)`, accountID, result.ID, r.Line, r.Date, r.Posted, r.Description,
			r.Merchant, r.Category, r.Type, r.Status, r.Time, r.Memo, r.Card, r.Cents, k[:])
		if err != nil {
			return Result{}, fmt.Errorf("line %d: %w", r.Line, err)
		}
	}
	result.Added = len(keep)
	if err = tx.Commit(); err != nil {
		return Result{}, err
	}
	return result, nil
}
