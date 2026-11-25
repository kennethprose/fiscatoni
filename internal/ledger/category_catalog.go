package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

type Category struct {
	ID           int64  `json:"id,string"`
	Name         string `json:"name"`
	Transactions int64  `json:"transactions"`
	Rules        int64  `json:"rules"`
}

var ErrInvalidCategoryName = errors.New("Enter a category name of up to 128 characters.")
var ErrDuplicateCategory = errors.New("A category with this name already exists.")
var ErrCategoryNotFound = errors.New("This category no longer exists.")
var ErrCategoryInUse = errors.New("Move transactions to another category or clear their category before deleting this one.")

func categoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		return "", ErrInvalidCategoryName
	}
	return name, nil
}

func isDuplicateKey(err error) bool {
	var conflict *mysql.MySQLError
	return errors.As(err, &conflict) && conflict.Number == 1062
}

func migrateCategories(ctx context.Context, db *sql.DB) error {
	const migration = "category-catalog"
	var done bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ledger_migrations WHERE name=?)", migration).Scan(&done); err != nil || done {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The marker is committed with the backfill and serializes concurrent startup requests.
	if _, err := tx.ExecContext(ctx, "INSERT INTO ledger_migrations(name) VALUES(?)", migration); err != nil {
		if isDuplicateKey(err) {
			return nil
		}
		return err
	}
	for _, table := range []string{"transactions", "merchant_categories", "merchant_regex_rules"} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO categories(name) SELECT ANY_VALUE(category) FROM "+table+
			" WHERE OCTET_LENGTH(category) > 0 GROUP BY SHA2(CAST(category AS BINARY), 256) ON DUPLICATE KEY UPDATE name=categories.name"); err != nil {
			return fmt.Errorf("backfill categories from %s: %w", table, err)
		}
	}
	return tx.Commit()
}

// Register the name before saving its references. The upsert holds the category
// lock until commit so category deletion or renaming sees the new references.
func registerCategory(ctx context.Context, tx *sql.Tx, name string) error {
	if name == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO categories(name) VALUES(?) ON DUPLICATE KEY UPDATE name=categories.name", name)
	return err
}

func Categories(ctx context.Context, db *sql.DB) ([]Category, error) {
	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `WITH usage_counts AS (
		SELECT UNHEX(SHA2(CAST(category AS BINARY), 256)) AS name_key, COUNT(*) AS transactions, 0 AS rules
			FROM transactions GROUP BY 1
		UNION ALL
		SELECT UNHEX(SHA2(CAST(category AS BINARY), 256)), 0, COUNT(*) FROM merchant_categories GROUP BY 1
		UNION ALL
		SELECT UNHEX(SHA2(CAST(category AS BINARY), 256)), 0, COUNT(*) FROM merchant_regex_rules GROUP BY 1
	), totals AS (
		SELECT name_key, SUM(transactions) AS transactions, SUM(rules) AS rules FROM usage_counts GROUP BY 1
	)
	SELECT c.id,c.name,COALESCE(t.transactions,0),COALESCE(t.rules,0)
		FROM categories c LEFT JOIN totals t USING(name_key) ORDER BY CAST(c.name AS BINARY)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	categories := []Category{}
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.Name, &category.Transactions, &category.Rules); err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	return categories, rows.Err()
}

func CreateCategory(ctx context.Context, db *sql.DB, rawName string) error {
	name, err := categoryName(rawName)
	if err != nil {
		return err
	}
	if err := ensureSchema(ctx, db); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "INSERT INTO categories(name) VALUES(?)", name)
	if isDuplicateKey(err) {
		return ErrDuplicateCategory
	}
	return err
}

func RenameCategory(ctx context.Context, db *sql.DB, id int64, rawName string) error {
	name, err := categoryName(rawName)
	if err != nil {
		return err
	}
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	oldName, err := lockCategory(ctx, tx, id)
	if err != nil {
		return err
	}
	if name == oldName {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, "UPDATE categories SET name=? WHERE id=?", name, id); err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicateCategory
		}
		return err
	}
	for _, table := range []string{"merchant_categories", "merchant_regex_rules", "transactions"} {
		if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET category=? WHERE CAST(category AS BINARY)=CAST(? AS BINARY)", name, oldName); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func DeleteCategory(ctx context.Context, db *sql.DB, id int64) error {
	tx, err := beginLedgerWideChange(ctx, db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	name, err := lockCategory(ctx, tx, id)
	if err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM transactions WHERE CAST(category AS BINARY)=CAST(? AS BINARY))", name).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrCategoryInUse
	}
	for _, table := range []string{"merchant_categories", "merchant_regex_rules"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE CAST(category AS BINARY)=CAST(? AS BINARY)", name); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM categories WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func lockCategory(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, "SELECT name FROM categories WHERE id=? FOR UPDATE", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrCategoryNotFound
	}
	return name, err
}
