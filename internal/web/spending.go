package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"fiscatoni/internal/ledger"
)

func spendingMonth(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	const layout = "2006-01"
	month, err := time.Parse(layout, raw)
	if err != nil || month.Year() < 1000 || month.Format(layout) != raw {
		return time.Time{}, errors.New("invalid month")
	}
	return month, nil
}

func spendingHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		var account int64
		if raw := values.Get("account"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				problem(w, 400, "Select a valid account.")
				return
			}
			account = id
		}
		months := 6
		if raw := values.Get("months"); raw != "" {
			count, err := strconv.Atoi(raw)
			if err != nil || count < 1 || count > (9999-1000+1)*12 {
				problem(w, 400, "Enter a valid number of months.")
				return
			}
			months = count
		}
		start, err := spendingMonth(values.Get("start"))
		if err != nil {
			problem(w, 400, "Enter a valid starting month.")
			return
		}
		end, err := spendingMonth(values.Get("end"))
		if err != nil {
			problem(w, 400, "Enter a valid ending month.")
			return
		}
		if !start.IsZero() && (end.IsZero() || start.After(end)) {
			problem(w, 400, "Choose an ending month on or after the starting month.")
			return
		}
		report, err := ledger.Spending(r.Context(), db, account, start, end, months)
		if err != nil {
			problem(w, 503, "Could not load spending. Try again.")
			return
		}
		writeJSON(w, 200, report)
	}
}
