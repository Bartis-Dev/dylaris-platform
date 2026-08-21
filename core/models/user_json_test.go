package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every User read populates Password (userSelectCols selects the column), so
// with `json:"password,omitempty"` the hash left the process unless the call
// site remembered to blank it first. Exactly two did. TOTPSecret and
// TOTPBackupCodes right beside it were already json:"-" - this pins that the
// bcrypt hash is now safe the same way, by the type rather than by memory.
func TestUserNeverSerializesItsSecrets(t *testing.T) {
	u := User{
		ID:              "11111111-2222-3333-4444-555555555555",
		Username:        "tester",
		Email:           "tester@example.com",
		Password:        "$2a$12$abcdefghijklmnopqrstuvTHISISTHEHASH",
		TOTPSecret:      "JBSWY3DPEHPK3PXP",
		TOTPBackupCodes: `["$2a$10$backupcodehash"]`,
	}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)

	for _, secret := range []string{u.Password, u.TOTPSecret, u.TOTPBackupCodes} {
		if strings.Contains(body, secret) {
			t.Fatalf("a secret reached the JSON body: %s", body)
		}
	}
	for _, field := range []string{`"password"`, `"totpSecret"`, `"totpBackupCodes"`} {
		if strings.Contains(body, field) {
			t.Fatalf("field %s is present in the JSON body: %s", field, body)
		}
	}
	// The rest of the row must still serialize, or this would be a regression
	// dressed up as a fix.
	if !strings.Contains(body, `"username":"tester"`) || !strings.Contains(body, u.ID) {
		t.Fatalf("ordinary fields went missing: %s", body)
	}
}
