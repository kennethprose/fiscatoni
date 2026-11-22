// Package web serves the local CSV import workflow. The browser retains the
// selected file; the server doesn't need upload storage or a session cache.
package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"

	"fiscatoni/internal/importer"
	"fiscatoni/internal/ledger"
)

//go:embed static/*
var assets embed.FS

type saveFunc func(context.Context, *sql.DB, int64, importer.Batch, map[int]bool, map[int]bool) (ledger.Result, error)
type categoryLookup func(context.Context, *sql.DB, []importer.Row) (ledger.CategoryOptions, error)

func Handler(db *sql.DB) http.Handler {
	return handler(db, ledger.SaveToAccount, ledger.CategoriesForReview)
}

func handler(db *sql.DB, save saveFunc, categories categoryLookup) http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /imports", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "imports.html")
	})
	mux.HandleFunc("GET /transactions", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "transactions.html")
	})
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "settings.html")
	})
	mux.HandleFunc("POST /api/settings/reset", resetHandler(db))
	mux.HandleFunc("GET /api/settings/merchant-rules", merchantRulesHandler(db))
	mux.HandleFunc("POST /api/settings/merchant-rules", merchantRuleWriteHandler(db))
	mux.HandleFunc("POST /api/settings/merchant-rules/test", merchantRuleTestHandler(db))
	mux.HandleFunc("POST /api/settings/merchant-rules/order", merchantRuleOrderHandler(db))
	mux.HandleFunc("PATCH /api/settings/merchant-rules/{id}", merchantRuleWriteHandler(db))
	mux.HandleFunc("DELETE /api/settings/merchant-rules/{id}", merchantRuleWriteHandler(db))
	mux.HandleFunc("GET /api/spending", spendingHandler(db))
	mux.HandleFunc("GET /api/transactions", transactionHandler(db))
	mux.HandleFunc("PATCH /api/transactions/{id}", transactionEditHandler(db))
	mux.HandleFunc("DELETE /api/transactions/{id}", transactionEditHandler(db))
	mux.HandleFunc("GET /api/transaction-categories", func(w http.ResponseWriter, r *http.Request) {
		names, err := ledger.TransactionCategories(r.Context(), db)
		if err != nil {
			problem(w, 503, "Could not load transaction categories. Try again.")
			return
		}
		writeJSON(w, 200, names)
	})
	mux.HandleFunc("GET /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		accounts, err := ledger.Accounts(r.Context(), db)
		if err != nil {
			problem(w, 503, "Could not load accounts. Try again.")
			return
		}
		writeJSON(w, 200, accounts)
	})
	mux.HandleFunc("POST /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "import" {
			problem(w, 403, "Open the local account manager and try again.")
			return
		}
		var input struct{ Bank, Label string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			problem(w, 400, "Invalid account details.")
			return
		}
		account, err := ledger.AddAccount(r.Context(), db, input.Bank, input.Label)
		if errors.Is(err, ledger.ErrAccountDetails) {
			problem(w, 422, err.Error())
			return
		}
		if err != nil {
			problem(w, 503, "Could not save the account. Try again.")
			return
		}
		writeJSON(w, 200, account)
	})
	for _, op := range []string{"inspect", "preview", "save"} {
		mux.HandleFunc("POST /api/"+op, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Fiscatoni") != "import" {
				problem(w, 403, "Open the local import screen and try again.")
				return
			}
			input, err := readUpload(w, r)
			if err != nil {
				problem(w, 400, err.Error())
				return
			}
			if op == "inspect" {
				ins, err := importer.Inspect(input.data)
				if err != nil {
					problem(w, 422, err.Error())
					return
				}
				writeJSON(w, 200, ins)
				return
			}
			var profile importer.Profile
			if err := json.Unmarshal([]byte(input.fields["profile"]), &profile); err != nil {
				problem(w, 400, "Invalid column mapping.")
				return
			}
			b, err := importer.Parse(input.data, &profile)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			encoded, _ := json.Marshal(profile)
			fingerprint := sha256.Sum256(append(b.Digest[:], encoded...))
			review := hex.EncodeToString(fingerprint[:])
			if op == "preview" {
				// Send cents as text to preserve all int64 values in JavaScript.
				type previewRow struct {
					importer.Row
					Cents         string
					MerchantGroup int
				}
				rows := make([]previewRow, len(b.Rows))
				groups := make(map[string]int)
				for i, row := range b.Rows {
					merchant := row.MerchantIdentity()
					group, exists := groups[merchant]
					if !exists {
						group = len(groups) + 1
						groups[merchant] = group
					}
					rows[i] = previewRow{row, strconv.FormatInt(row.Cents, 10), group}
				}
				options, err := categories(r.Context(), db, b.Rows)
				if err != nil {
					problem(w, 503, "Could not load saved categories. Try the preview again.")
					return
				}
				writeJSON(w, 200, map[string]any{"rows": rows, "review": review, "categories": options})
				return
			}
			if input.fields["review"] != review {
				problem(w, 409, "Preview this mapping before saving.")
				return
			}
			account, err := strconv.ParseInt(input.fields["accountID"], 10, 64)
			if err != nil || account <= 0 {
				problem(w, 422, ledger.ErrInvalidAccount.Error())
				return
			}
			if err := applyCategories(b.Rows, input.fields["categories"]); err != nil {
				problem(w, 422, err.Error())
				return
			}
			var decisions map[int]bool
			if raw := input.fields["decisions"]; raw != "" {
				if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
					problem(w, 400, "Invalid overlap decisions.")
					return
				}
			}
			var remember map[int]bool
			if raw := input.fields["remember"]; raw != "" {
				if err := json.Unmarshal([]byte(raw), &remember); err != nil {
					problem(w, 422, "Invalid merchant selections.")
					return
				}
				valid := 0
				for _, row := range b.Rows {
					if remember[row.Line] && row.Category != "" && len([]rune(row.Category)) <= 128 {
						valid++
					}
				}
				if valid != len(remember) {
					problem(w, 422, "Choose a category for each merchant you want to remember.")
					return
				}
			}
			result, err := save(r.Context(), db, account, b, decisions, remember)
			if errors.Is(err, ledger.ErrMerchantConflict) {
				problem(w, 422, err.Error())
				return
			}
			if errors.Is(err, ledger.ErrInvalidAccount) {
				problem(w, 422, err.Error())
				return
			}
			var overlap *ledger.OverlapError
			if errors.As(err, &overlap) {
				writeJSON(w, 409, map[string]any{"error": "Choose whether to keep or skip each possible duplicate.", "lines": overlap.Lines})
				return
			}
			if err != nil {
				problem(w, 503, "Could not complete the save. Check MySQL and retry with the same file and account.")
				return
			}
			writeJSON(w, 200, result)
		})
	}
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "Local access only", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		protected.ServeHTTP(w, r)
	})
}

// Category assignments refer to physical CSV lines, including on later pages.
func applyCategories(rows []importer.Row, raw string) error {
	if raw == "" {
		return nil
	}
	var edits map[int]string
	if err := json.Unmarshal([]byte(raw), &edits); err != nil {
		return errors.New("Invalid category assignments.")
	}
	for i, row := range rows {
		category, ok := edits[row.Line]
		if !ok {
			continue
		}
		category = strings.TrimSpace(category)
		if len([]rune(category)) > 128 && category != row.Category {
			return errors.New("Use a category of up to 128 characters.")
		}
		rows[i].Category = category
		delete(edits, row.Line)
	}
	if len(edits) > 0 {
		return errors.New("A category refers to a row outside this preview.")
	}
	return nil
}

type upload struct {
	data   []byte
	fields map[string]string
}

func readUpload(w http.ResponseWriter, r *http.Request) (upload, error) {
	const maxFile = 32 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxFile+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		return upload{}, errors.New("Expected a CSV upload.")
	}
	in := upload{fields: map[string]string{}}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return in, errors.New("Incomplete upload or file exceeds 32 MiB.")
		}
		name := part.FormName()
		limit := int64(1 << 20)
		if name == "file" {
			limit = maxFile
		}
		value, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil || int64(len(value)) > limit {
			return in, errors.New("Upload exceeds the size limit.")
		}
		if name == "file" {
			in.data = value
		} else {
			in.fields[name] = string(value)
		}
	}
	if len(in.data) == 0 {
		return in, errors.New("Choose a nonempty CSV file.")
	}
	return in, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
