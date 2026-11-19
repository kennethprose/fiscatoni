package importer

import (
	"strings"
	"testing"
)

func TestBankExports(t *testing.T) {
	cases := []struct {
		bank, csv          string
		purchase, payment  int64
		category, merchant string
	}{
		{"chase", "Transaction Date,Post Date,Description,Category,Type,Amount,Memo\n04/05/2026,04/06/2026,\"Sample, Shop\",Food,Sale,-7.09,\n04/05/2026,04/06/2026,Test payment,,Payment,9,\n", -709, 900, "Food", ""},
		{"capitalone", "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n2026-04-05,2026-04-06,0000,Sample shop,Food,7.09,\n2026-04-05,2026-04-06,0000,Test credit,Payment/Credit,,9\n", -709, 900, "Food", ""},
		{"robinhood-card", "Date,Time,Cardholder,Amount,Points,Balance,Status,Type,Merchant,Description\n2026-04-05,1:00 PM,Example,7.09,0,0,Posted,Purchase,Sample shop,Demo\n2026-04-05,2:00 PM,Example,-9,0,0,Posted,Payment,Sample bank,Demo\n", -709, 900, "", "Sample shop"},
	}
	for _, c := range cases {
		t.Run(c.bank, func(t *testing.T) {
			b, err := Parse([]byte("\ufeff"+strings.ReplaceAll(c.csv, "\n", "\r\n")), nil)
			if err != nil {
				t.Fatal(err)
			}
			if b.Bank != c.bank || len(b.Rows) != 2 {
				t.Fatalf("unexpected batch %v", b)
			}
			r := b.Rows[0]
			if r.Date != "2026-04-05" || r.Line != 2 || r.Cents != c.purchase || b.Rows[1].Cents != c.payment ||
				r.Category != c.category || r.Merchant != c.merchant {
				t.Fatalf("unexpected rows %+v", b.Rows)
			}
		})
	}
}

func TestCustomMapping(t *testing.T) {
	p := Profile{Bank: "example", Date: "When", Layout: "2006-01-02", Description: "Details", Amount: "Value"}
	csv := " value ;DETAILS;when\n1.2;\"A;B\nC\";2026-02-01\n-1;D;2026-02-01\n"
	if _, err := Parse([]byte(csv), nil); err == nil {
		t.Fatal("guessed unknown layout")
	}
	b, err := Parse([]byte(csv), &p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rows[0].Cents != 120 || b.Rows[0].Description != "A;B\nC" || b.Rows[1].Line != 4 {
		t.Fatal(b.Rows)
	}
}

func TestRejectsMalformedBatch(t *testing.T) {
	header := "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n"
	valid := "2026-04-05,2026-04-06,0000,A,Food,1,\n"
	for _, bad := range []string{
		"2026-02-30,2026-04-06,0000,A,Food,1,",
		"2026-04-05,2026-04-06,0000,A,Food,1,2",
		"2026-04-05,2026-04-06,0000,A,Food,,",
		"2026-04-05,2026-04-06,0000,A,Food,-1,",
		"2026-04-05,2026-04-06,0000,A,Food,1.005,",
		"2026-04-05,2026-04-06,0000,A,Food",
	} {
		b, err := Parse([]byte(header+valid+bad), nil)
		if err == nil || len(b.Rows) != 0 {
			t.Fatalf("partial or unvalidated batch: %+v", b)
		}
	}
}
