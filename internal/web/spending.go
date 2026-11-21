package web

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"fiscatoni/internal/ledger"
)

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
			if err != nil || count < 1 || count > 12 {
				problem(w, 400, "Select a period of up to 12 months.")
				return
			}
			months = count
		}
		var end time.Time
		if raw := values.Get("end"); raw != "" {
			month, err := time.Parse("2006-01", raw)
			if err != nil || month.Year() < 1000 || month.Format("2006-01") != raw {
				problem(w, 400, "Enter a valid ending month.")
				return
			}
			end = month
		}
		report, err := ledger.Spending(r.Context(), db, account, end, months)
		if err != nil {
			problem(w, 503, "Could not load spending. Try again.")
			return
		}
		writeJSON(w, 200, report)
	}
}
