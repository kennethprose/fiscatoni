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
	`CREATE TABLE IF NOT EXISTS accounts (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, bank VARCHAR(64) NOT NULL, label VARCHAR(128) NOT NULL,
		currency CHAR(3) NOT NULL DEFAULT 'USD', UNIQUE(bank,label)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS imports (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, account_id BIGINT NOT NULL, file_hash BINARY(32) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(account_id,file_hash), UNIQUE(account_id,id), FOREIGN KEY(account_id) REFERENCES accounts(id)
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
	for _, ddl := range schema {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return Result{}, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	// The unique-key update takes the same account lock for concurrent imports.
	res, err := tx.ExecContext(ctx, `INSERT INTO accounts(bank,label) VALUES(?,?)
		ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`, batch.Bank, account)
	if err != nil {
		return Result{}, err
	}
	accountID, err := res.LastInsertId()
	if err != nil {
		return Result{}, err
	}
	var result Result
	err = tx.QueryRowContext(ctx, "SELECT id FROM imports WHERE account_id=? AND file_hash=?", accountID, batch.Digest[:]).Scan(&result.ID)
	if err == nil {
		result.AlreadyImported = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}

	// Check only previously committed rows, so matching purchases in one file survive.
	var keep []importer.Row
	var unresolved []string
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
				unresolved = append(unresolved, strconv.Itoa(r.Line))
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
		return Result{}, fmt.Errorf("possible overlap on CSV lines %s; review with preview -rows, then use -keep-lines or -skip-lines; no rows saved", strings.Join(unresolved, ","))
	}
	if used != len(decisions) {
		return Result{}, errors.New("line decision does not match an overlapping row; no rows saved")
	}
	res, err = tx.ExecContext(ctx, "INSERT INTO imports(account_id,file_hash) VALUES(?,?)", accountID, batch.Digest[:])
	if err != nil {
		return Result{}, err
	}
	result.ID, err = res.LastInsertId()
	if err != nil {
		return Result{}, err
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
