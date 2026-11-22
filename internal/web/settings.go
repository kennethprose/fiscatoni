package web

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"fiscatoni/internal/ledger"
)

func merchantRulesHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules, err := ledger.MerchantRules(r.Context(), db)
		if err != nil {
			problem(w, 503, "Could not load merchant rules. Try again.")
			return
		}
		writeJSON(w, 200, rules)
	}
}

func regexRuleID(value string) (int64, bool) {
	raw, hasPrefix := strings.CutPrefix(value, "regex-")
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, hasPrefix && err == nil && id > 0
}

func merchantRuleOrderHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "settings" {
			problem(w, 403, "Open Settings to reorder merchant rules.")
			return
		}
		var request struct {
			First  string `json:"first"`
			Second string `json:"second"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
			problem(w, 400, "Select two adjacent regex rules.")
			return
		}
		first, validFirst := regexRuleID(request.First)
		second, validSecond := regexRuleID(request.Second)
		if !validFirst || !validSecond || first == second {
			problem(w, 400, "Select two adjacent regex rules.")
			return
		}
		err := ledger.SwapRegexRuleOrder(r.Context(), db, first, second)
		if errors.Is(err, ledger.ErrMerchantRuleOrderChanged) {
			problem(w, 409, err.Error())
			return
		}
		if err != nil {
			problem(w, 503, "Could not save the rule order. Try again.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func merchantRuleTestHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "settings" {
			problem(w, 403, "Open Settings to test a merchant rule.")
			return
		}
		var request struct {
			Pattern string `json:"pattern"`
			Page    int32  `json:"page"`
			RuleID  string `json:"ruleId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF || request.Page < 0 {
			problem(w, 400, "Enter a regular expression to test.")
			return
		}
		var id int64
		if request.RuleID != "" {
			var valid bool
			id, valid = regexRuleID(request.RuleID)
			if !valid {
				problem(w, 400, "Select a valid regex rule.")
				return
			}
		}
		preview, err := ledger.PreviewMerchantPattern(r.Context(), db, strings.TrimSpace(request.Pattern), int(max(1, request.Page)), id)
		if errors.Is(err, ledger.ErrMerchantRuleNotFound) {
			problem(w, 404, err.Error())
			return
		}
		if errors.Is(err, ledger.ErrInvalidMerchantPattern) {
			problem(w, 422, err.Error())
			return
		}
		if err != nil {
			problem(w, 503, "Could not test this pattern. Try again.")
			return
		}
		writeJSON(w, 200, preview)
	}
}

func merchantRuleWriteHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fiscatoni") != "settings" {
			problem(w, 403, "Open Settings to manage merchant rules.")
			return
		}
		var details struct {
			Category       string  `json:"category"`
			Kind           string  `json:"kind"`
			Match          *string `json:"match"`
			ApplyToHistory bool    `json:"applyToHistory"`
		}
		if r.Method != http.MethodDelete {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			if err := decoder.Decode(&details); err != nil || decoder.Decode(new(any)) != io.EOF {
				problem(w, 400, "Enter a category for this merchant rule.")
				return
			}
			details.Category = strings.TrimSpace(details.Category)
			if details.Category == "" || utf8.RuneCountInString(details.Category) > 128 {
				problem(w, 422, "Enter a category of up to 128 characters.")
				return
			}
			if details.Match != nil {
				*details.Match = strings.TrimSpace(*details.Match)
			}
		}
		var err error
		var updated int64
		if r.Method == http.MethodPost {
			if details.Match == nil || *details.Match == "" || utf8.RuneCountInString(*details.Match) > 512 {
				problem(w, 422, "Enter a merchant name or pattern of up to 512 characters.")
				return
			}
			switch details.Kind {
			case "exact", "regex":
				updated, err = ledger.CreateMerchantRule(r.Context(), db, *details.Match, details.Category, details.Kind == "regex", details.ApplyToHistory)
			default:
				problem(w, 422, "Choose Exact merchant name or Regular expression.")
				return
			}
		} else if strings.HasPrefix(r.PathValue("id"), "regex-") {
			id, valid := regexRuleID(r.PathValue("id"))
			if !valid {
				problem(w, 400, "Select a valid merchant rule.")
				return
			}
			if r.Method == http.MethodDelete {
				err = ledger.DeleteRegexMerchantRule(r.Context(), db, id)
			} else {
				updated, err = ledger.UpdateRegexMerchantRule(r.Context(), db, id, details.Match, details.Category, details.ApplyToHistory)
			}
		} else {
			key, parseErr := hex.DecodeString(r.PathValue("id"))
			if parseErr != nil || len(key) != 32 {
				problem(w, 400, "Select a valid merchant rule.")
				return
			}
			if r.Method == http.MethodDelete {
				err = ledger.DeleteMerchantRule(r.Context(), db, [32]byte(key))
			} else {
				updated, err = ledger.UpdateMerchantRule(r.Context(), db, [32]byte(key), details.Category, details.ApplyToHistory)
			}
		}
		if errors.Is(err, ledger.ErrMerchantRuleNotFound) {
			problem(w, 404, err.Error())
			return
		}
		if errors.Is(err, ledger.ErrDuplicateMerchantRule) {
			problem(w, 409, err.Error())
			return
		}
		if errors.Is(err, ledger.ErrInvalidMerchantPattern) {
			problem(w, 422, err.Error())
			return
		}
		if err != nil {
			problem(w, 503, "Could not update the merchant rule. Try again.")
			return
		}
		if r.Method != http.MethodDelete && details.ApplyToHistory {
			writeJSON(w, 200, map[string]int64{"updated": updated})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

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
