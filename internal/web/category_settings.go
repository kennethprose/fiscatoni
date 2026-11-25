package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"fiscatoni/internal/ledger"
)

func categoryListHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		categories, err := ledger.Categories(r.Context(), db)
		if err != nil {
			problem(w, 503, "Could not load categories. Try again.")
			return
		}
		writeJSON(w, 200, categories)
	}
}

func categoryWriteHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "settings" {
			problem(w, 403, "Open Settings to manage categories.")
			return
		}
		var id int64
		if r.Method != http.MethodPost {
			var err error
			id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil || id <= 0 {
				problem(w, 400, "Select a valid category.")
				return
			}
		}
		var input struct {
			Name string `json:"name"`
		}
		if r.Method != http.MethodDelete {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
				problem(w, 400, "Enter a category name.")
				return
			}
		}
		var err error
		switch r.Method {
		case http.MethodPost:
			err = ledger.CreateCategory(r.Context(), db, input.Name)
		case http.MethodPatch:
			err = ledger.RenameCategory(r.Context(), db, id, input.Name)
		case http.MethodDelete:
			err = ledger.DeleteCategory(r.Context(), db, id)
		}
		switch {
		case errors.Is(err, ledger.ErrCategoryNotFound):
			problem(w, 404, err.Error())
		case errors.Is(err, ledger.ErrDuplicateCategory), errors.Is(err, ledger.ErrCategoryInUse):
			problem(w, 409, err.Error())
		case errors.Is(err, ledger.ErrInvalidCategoryName):
			problem(w, 422, err.Error())
		case err != nil:
			problem(w, 503, "Could not save the category change. Try again.")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}
