package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"fiscatoni/internal/importer"
	"fiscatoni/internal/ledger"
)

func TestReviewBeforeSave(t *testing.T) {
	const csv = "When;Payee;Value\n2026-06-01;Example merchant;3.14\n"
	const mapping = `{"Bank":"example","Date":"When","Description":"Payee","Amount":"Value","Layout":"2006-01-02"}`
	calls := 0
	h := handler(nil, func(_ context.Context, _ *sql.DB, account int64, b importer.Batch, decisions, remember map[int]bool) (ledger.Result, error) {
		calls++
		if account != 7 || len(b.Rows) != 1 || b.Rows[0].Cents != 314 || b.Rows[0].Category != "Food" || !remember[2] {
			t.Fatal("incorrect saved batch")
		}
		if !decisions[2] {
			return ledger.Result{}, &ledger.OverlapError{Lines: []int{2}}
		}
		return ledger.Result{Added: 1, ID: 1}, nil
	}, func(context.Context, *sql.DB, []importer.Row) (ledger.CategoryOptions, error) {
		return ledger.CategoryOptions{}, nil
	})
	post := func(action string, values map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		m := multipart.NewWriter(&body)
		part, err := m.CreateFormFile("file", "sample.csv")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(csv))
		for key, value := range values {
			m.WriteField(key, value)
		}
		m.Close()
		req := httptest.NewRequest(http.MethodPost, "http://localhost/api/"+action, &body)
		req.Header.Set("Content-Type", m.FormDataContentType())
		req.Header.Set("X-Fiscatoni", "import")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	ins := post("inspect", nil)
	var description importer.Inspection
	if json.Unmarshal(ins.Body.Bytes(), &description) != nil || len(description.Headers) != 3 || description.Profile != nil || description.Sample[1] != "Example merchant" {
		t.Fatal(ins.Body.String())
	}
	fields := map[string]string{"profile": mapping, "accountID": "7", "categories": `{"2":"Food"}`, "remember": `{"2":true}`}
	preview := post("preview", fields)
	var view struct {
		Review string
		Rows   []struct{ Cents string }
	}
	if json.Unmarshal(preview.Body.Bytes(), &view) != nil || view.Review == "" || view.Rows[0].Cents != "314" {
		t.Fatal(preview.Body.String())
	}
	if res := post("save", fields); res.Code != 409 || calls != 0 {
		t.Fatal("unreviewed save")
	}
	fields["review"] = view.Review
	fields["profile"] = mapping[:len(mapping)-1] + `,"Invert":true}`
	if res := post("save", fields); res.Code != 409 || calls != 0 {
		t.Fatal("stale mapping saved")
	}
	fields["profile"] = mapping
	conflict := post("save", fields)
	if conflict.Code != 409 || !bytes.Contains(conflict.Body.Bytes(), []byte(`"lines":[2]`)) {
		t.Fatal(conflict.Body.String())
	}
	fields["decisions"] = `{"2":true}`
	if res := post("save", fields); res.Code != 200 || calls != 2 {
		t.Fatal(res.Body.String())
	}
}
