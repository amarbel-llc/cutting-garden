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

func TestAccount_ValidatePassword_PasswordEnvAlone_OK(t *testing.T) {
	a := Account{PasswordEnv: "FOO"}
	if err := a.ValidatePassword(); err != nil {
		t.Errorf("password_env alone should validate, got %v", err)
	}
}

func TestAccount_ValidatePassword_SourceAndKey_OK(t *testing.T) {
	a := Account{PasswordSource: "piggy", PasswordKey: "path/to/entry"}
	if err := a.ValidatePassword(); err != nil {
		t.Errorf("password_source+password_key should validate, got %v", err)
	}
}

func TestAccount_ValidatePassword_Empty_OK(t *testing.T) {
	if err := (Account{}).ValidatePassword(); err != nil {
		t.Errorf("no password fields should validate (unauthenticated), got %v", err)
	}
}

func TestAccount_ValidatePassword_EnvAndSourceConflict(t *testing.T) {
	a := Account{PasswordEnv: "FOO", PasswordSource: "env", PasswordKey: "FOO"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_env set together with password_source/password_key")
	}
}

func TestAccount_ValidatePassword_EnvAndKeyOnlyConflict(t *testing.T) {
	a := Account{PasswordEnv: "FOO", PasswordKey: "FOO"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_env set together with password_key")
	}
}

func TestAccount_ValidatePassword_SourceWithoutKey(t *testing.T) {
	a := Account{PasswordSource: "piggy"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_source set without password_key")
	}
}

func TestAccount_ValidatePassword_KeyWithoutSource(t *testing.T) {
	a := Account{PasswordKey: "x"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_key set without password_source")
	}
}

func TestAccount_ValidatePassword_UnknownSource(t *testing.T) {
	a := Account{PasswordSource: "keychain", PasswordKey: "x"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: unknown password_source")
	}
}
