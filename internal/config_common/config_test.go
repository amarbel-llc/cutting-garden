package config_common

import "testing"

func TestAccount_NewPasswordFieldsRoundTripThroughTOML(t *testing.T) {
	section := AccountsSection{
		Accounts: []Account{{
			Root:           Root{Name: "x", URL: "caldav://host/dav/"},
			PasswordSource: "piggy",
			PasswordKey:    "cutting-garden/caldav/x",
		}},
	}

	doc, err := DecodeAccountsSection([]byte(""))
	if err != nil {
		t.Fatalf("DecodeAccountsSection (empty): %v", err)
	}
	*doc.Data() = section
	encoded, err := doc.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeAccountsSection(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := decoded.Data().Accounts[0]
	if got.PasswordSource != "piggy" || got.PasswordKey != "cutting-garden/caldav/x" {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}
