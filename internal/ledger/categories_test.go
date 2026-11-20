package ledger

import (
	"testing"

	"fiscatoni/internal/importer"
)

func TestMerchantMatchingAcrossFormats(t *testing.T) {
	card := importer.Row{Merchant: "Corner Shop", Description: "Terminal 42"}
	statement := importer.Row{Description: " CORNER   shop "}
	if merchantKey(card) != merchantKey(statement) {
		t.Fatal("merchant and equivalent statement description should match")
	}
	for _,name:=range []string{"Corner Shop East","Terminal 42"} {
		if merchantKey(card)==merchantKey(importer.Row{Description:name}) {
			t.Fatalf("unexpected merchant match for %q",name)
		}
	}
}
