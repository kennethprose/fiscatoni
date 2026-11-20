// Package importer recognizes bank exports and prepares ledger rows without database access.
package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Profile names source columns. Dates use Go layouts, such as 2006-01-02.
type Profile struct {
	Bank, Date, Layout, Posted, Description  string
	Amount, Debit, Credit, Category          string
	Merchant, Type, Status, Time, Memo, Card string
	Invert                                   bool // A positive signed amount is a charge on this export.
}

var profiles = []Profile{
	{Bank: "chase", Date: "Transaction Date", Layout: "01/02/2006", Posted: "Post Date",
		Description: "Description", Category: "Category", Type: "Type", Amount: "Amount", Memo: "Memo"},
	{Bank: "capitalone", Date: "Transaction Date", Layout: "2006-01-02", Posted: "Posted Date",
		Description: "Description", Category: "Category", Debit: "Debit", Credit: "Credit", Card: "Card No."},
	{Bank: "robinhood", Date: "Date", Layout: "2006-01-02", Time: "Time", Description: "Description",
		Merchant: "Merchant", Amount: "Amount", Status: "Status", Type: "Type", Invert: true},
}

type Row struct {
	Line        int
	Date        string
	Posted      string
	Description string
	Merchant    string
	Category    string
	Type        string
	Status      string
	Time        string
	Memo        string
	Card        string
	Cents       int64
}

type Batch struct {
	Bank   string
	Digest [32]byte
	Rows   []Row
}

func (p Profile) validate() error {
	if strings.TrimSpace(p.Bank) == "" || len(p.Bank) > 64 || p.Date == "" || p.Layout == "" || p.Description == "" {
		return errors.New("mapping requires Bank (up to 64 bytes), Date, Layout and Description")
	}
	if p.Amount != "" && p.Debit == "" && p.Credit == "" {
		return nil
	}
	if p.Amount == "" && p.Debit != "" && p.Credit != "" && !strings.EqualFold(p.Debit, p.Credit) && !p.Invert {
		return nil
	}
	return errors.New("mapping requires one signed Amount column or separate Debit and Credit columns")
}

func key(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (p Profile) matches(header map[string]int) bool {
	for _, col := range []string{p.Date, p.Posted, p.Description, p.Amount, p.Debit, p.Credit, p.Category, p.Merchant, p.Type, p.Status, p.Time, p.Memo, p.Card} {
		if _, ok := header[key(col)]; col != "" && !ok {
			return false
		}
	}
	return true
}

// Inspection exposes the original header spelling for a mapping form. An
// unrecognized layout still has usable columns, even if its rows need mapping.
type Inspection struct {
	Headers []string
	Sample  []string
	Profile *Profile
}

func Inspect(data []byte) (Inspection, error) {
	ins, _, err := readHeader(data, profiles)
	return ins, err
}

func readHeader(data []byte, candidates []Profile) (Inspection, *csv.Reader, error) {
	if !utf8.Valid(data) {
		return Inspection{}, nil, errors.New("CSV must be UTF-8")
	}
	input := bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var best Inspection
	for _, sep := range []rune{',', ';', '\t'} {
		r := csv.NewReader(bytes.NewReader(input))
		r.Comma = sep
		header, err := r.Read()
		if err != nil || len(header) < 2 {
			continue
		}
		columns := make(map[string]int)
		for i, col := range header {
			k := key(col)
			if k == "" {
				continue
			}
			if _, exists := columns[k]; exists {
				return Inspection{}, nil, errors.New("duplicate CSV header")
			}
			columns[k] = i
		}
		ins := Inspection{Headers: header}
		for i := range candidates {
			if candidates[i].matches(columns) {
				if ins.Profile != nil {
					ins.Profile = nil
					break
				}
				ins.Profile = &candidates[i]
			}
		}
		sampleReader := csv.NewReader(bytes.NewReader(input[r.InputOffset():]))
		sampleReader.Comma = sep
		ins.Sample, _ = sampleReader.Read()
		if ins.Profile != nil {
			return ins, r, nil
		}
		if len(header) > len(best.Headers) {
			best = ins
		}
	}
	if len(best.Headers) == 0 {
		return best, nil, errors.New("expected a header with at least two CSV columns")
	}
	return best, nil, nil
}

func Parse(data []byte, custom *Profile) (Batch, error) {
	candidates := profiles
	if custom != nil {
		if err := custom.validate(); err != nil {
			return Batch{}, err
		}
		candidates = []Profile{*custom}
	}
	ins, r, err := readHeader(data, candidates)
	if err != nil {
		return Batch{}, err
	}
	if ins.Profile == nil {
		return Batch{}, errors.New("unrecognized CSV layout; supply a matching -mapping JSON file")
	}
	columns := make(map[string]int)
	for i, col := range ins.Headers {
		columns[key(col)] = i
	}
	p := ins.Profile
	batch := Batch{Bank: p.Bank, Digest: sha256.Sum256(data)}
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Batch{}, fmt.Errorf("invalid CSV: %w", err)
		}
		line, _ := r.FieldPos(0)
		get := func(col string) string {
			if col == "" {
				return ""
			}
			return strings.TrimSpace(record[columns[key(col)]])
		}
		row, err := p.parseRow(get)
		if err != nil {
			return Batch{}, fmt.Errorf("line %d: %w", line, err)
		}
		row.Line = line
		batch.Rows = append(batch.Rows, row)
	}
	if len(batch.Rows) == 0 {
		return Batch{}, errors.New("CSV contains no transactions")
	}
	return batch, nil
}

func (p Profile) parseRow(get func(string) string) (Row, error) {
	row := Row{Description: get(p.Description), Merchant: get(p.Merchant), Type: get(p.Type), Category: get(p.Category),
		Status: get(p.Status), Time: get(p.Time), Memo: get(p.Memo), Card: get(p.Card)}
	if row.Description == "" && row.Merchant == "" {
		return Row{}, errors.New("missing description and merchant")
	}
	date := func(col string) (string, error) {
		t, err := time.Parse(p.Layout, get(col))
		if err != nil || t.Year() < 1000 {
			return "", fmt.Errorf("invalid date in %s (expected %s)", col, p.Layout)
		}
		return t.Format("2006-01-02"), nil
	}
	var err error
	if row.Date, err = date(p.Date); err != nil {
		return Row{}, err
	}
	if get(p.Posted) != "" {
		if row.Posted, err = date(p.Posted); err != nil {
			return Row{}, err
		}
	}
	col, invert := p.Amount, p.Invert
	if col == "" {
		hasDebit, hasCredit := get(p.Debit) != "", get(p.Credit) != ""
		if hasDebit == hasCredit {
			return Row{}, errors.New("expected exactly one populated debit or credit")
		}
		if hasDebit {
			col, invert = p.Debit, true
		} else {
			col = p.Credit
		}
	}
	row.Cents, err = cents(get(col))
	if err != nil || (p.Amount == "" && row.Cents < 0) {
		return Row{}, fmt.Errorf("invalid monetary amount in %s", col)
	}
	if invert {
		row.Cents = -row.Cents
	}
	return row, nil
}

// Parse the magnitude separately so negation is always representable.
func cents(s string) (int64, error) {
	negative := strings.HasPrefix(s, "-")
	if negative || strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	whole, frac, dot := strings.Cut(s, ".")
	if whole == "" || (dot && frac == "") || len(frac) > 2 {
		return 0, errors.New("invalid decimal")
	}
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid digit")
		}
	}
	n, err := strconv.ParseInt(whole+frac+strings.Repeat("0", 2-len(frac)), 10, 64)
	if negative {
		n = -n
	}
	return n, err
}
