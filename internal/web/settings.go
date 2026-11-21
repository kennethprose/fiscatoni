package web

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"

	"fiscatoni/internal/ledger"
)

func resetHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "settings" {
			problem(w, 403, "Open Settings to confirm deleting all data.")
			return
		}
		var request struct {
			Confirmed bool `json:"confirmed"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		if err := decoder.Decode(&request); err != nil || !request.Confirmed || decoder.Decode(new(any)) != io.EOF {
			problem(w, 400, "Confirm that you want to delete all data.")
			return
		}
		if err := ledger.Reset(r.Context(), db); err != nil {
			problem(w, 503, "Could not complete the reset. Try again.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
