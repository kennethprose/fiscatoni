package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"fiscatoni/internal/ledger"
)

func transactionHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			problem(w, 400, "The filters could not be read.")
			return
		}
		filters, err := transactionFilters(values)
		if err != nil {
			problem(w, 400, err.Error())
			return
		}
		page, err := ledger.Transactions(r.Context(), db, filters)
		if err != nil {
			problem(w, 503, "Could not load transactions. Try again.")
			return
		}
		writeJSON(w, 200, page)
	}
}

func transactionEditHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "ledger" {
			problem(w, 403, "Open the local transaction dashboard and try again.")
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			problem(w, 400, "Select a valid transaction.")
			return
		}
		if r.Method == http.MethodDelete {
			err = ledger.DeleteTransaction(r.Context(), db, id)
		} else {
			var details struct{ Category *string }
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			if err := decoder.Decode(&details); err != nil || details.Category == nil || decoder.Decode(new(any)) != io.EOF {
				problem(w, 400, "Enter a category or select Uncategorized.")
				return
			}
			category := strings.TrimSpace(*details.Category)
			if utf8.RuneCountInString(category) > 128 {
				problem(w, 422, "Use a category of up to 128 characters.")
				return
			}
			err = ledger.UpdateTransactionCategory(r.Context(), db, id, category)
		}
		if errors.Is(err, ledger.ErrTransactionNotFound) {
			problem(w, 404, err.Error())
			return
		}
		if err != nil {
			problem(w, 503, "Could not update the ledger. Try again.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func transactionFilters(values url.Values) (ledger.TransactionFilters, error) {
	filters := ledger.TransactionFilters{
		From:      values.Get("from"),
		To:        values.Get("to"),
		Search:    strings.TrimSpace(values.Get("search")),
		Merchant:  values.Get("merchant"),
		Direction: values.Get("direction"),
		Page:      1,
	}
	if values.Has("category") {
		category := values.Get("category")
		filters.Category = &category
	}
	if account := values.Get("account"); account != "" {
		id, err := strconv.ParseInt(account, 10, 64)
		if err != nil || id <= 0 {
			return filters, errors.New("Select a valid account.")
		}
		filters.AccountID = id
	}
	for _, date := range []string{filters.From, filters.To} {
		if date == "" {
			continue
		}
		parsed, err := time.Parse(time.DateOnly, date)
		if err != nil || parsed.Year() < 1000 || parsed.Format(time.DateOnly) != date {
			return filters, errors.New("Enter dates in YYYY-MM-DD format.")
		}
	}
	if filters.To != "" && filters.From > filters.To {
		return filters, errors.New("The end date must be on or after the start date.")
	}
	switch filters.Direction {
	case "", "in", "out", "spend", "income":
	default:
		return filters, errors.New("Select a valid direction.")
	}
	if utf8.RuneCountInString(filters.Search) > 200 {
		return filters, errors.New("Search supports up to 200 characters.")
	}
	if page := values.Get("page"); page != "" {
		number, err := strconv.Atoi(page)
		if err != nil || number < 1 {
			return filters, errors.New("Select a valid page.")
		}
		filters.Page = number
	}
	return filters, nil
}
