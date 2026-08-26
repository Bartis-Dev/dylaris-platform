package mailer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeSettings map[string]string

func (f fakeSettings) GetSetting(key string) (string, error) { return f[key], nil }

// The rule this decides: an install that predates the provider setting keeps
// sending mail.
//
// `mail.provider` does not exist in an existing database, and GetSetting answers
// "" for a key it has never seen. Reading that as an unknown provider would stop
// every verification mail, every password reset and every suspension notice on
// upgrade, with nothing in the UI changed to explain it.
func TestEmptyProviderMeansSMTP(t *testing.T) {
	s := fakeSettings{
		"smtp.default.host":       "smtp.example.com",
		"smtp.default.from_email": "noreply@example.com",
	}
	transport, err := Load(s, "auth")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.HasPrefix(transport.Describe(), "SMTP") {
		t.Errorf("Describe() = %q, want the SMTP transport", transport.Describe())
	}
}

func TestUnknownProviderIsRefused(t *testing.T) {
	_, err := Load(fakeSettings{SettingKeyProvider: "carrier-pigeon"}, "auth")
	if err == nil {
		t.Fatal("an unknown provider was accepted; mail would silently go nowhere")
	}
}

// Resend needs two things, and the failure mode for each is a message that
// leaves without a sender or with no credential - both of which Resend answers
// with a status code and a body nobody reads.
func TestResendRefusesToLoadWithoutWhatItNeeds(t *testing.T) {
	cases := []struct {
		name string
		s    fakeSettings
		want string
	}{
		{
			"no api key",
			fakeSettings{SettingKeyProvider: ProviderResend, "smtp.default.from_email": "a@b.com"},
			"api key",
		},
		{
			"no from address",
			fakeSettings{SettingKeyProvider: ProviderResend, SettingKeyResendAPIKey: "re_x"},
			"from address",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(c.s, "auth")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to name the missing %s", err, c.want)
			}
		})
	}
}

// The rule this decides: what actually goes on the wire, and that the credential
// travels in the header rather than the body.
func TestResendSendsWhatItShould(t *testing.T) {
	var gotAuth string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport, err := Load(fakeSettings{
		SettingKeyProvider:        ProviderResend,
		SettingKeyResendAPIKey:    "re_test_key",
		settingKeyResendBaseURL:   srv.URL,
		"smtp.default.from_email": "noreply@example.com",
		"smtp.default.from_name":  "Dylaris",
	}, "auth")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := transport.Send(Message{To: "user@example.com", Subject: "Hi", Body: "text"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if gotAuth != "Bearer re_test_key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if body["from"] != "Dylaris <noreply@example.com>" {
		t.Errorf("from = %v, want the display name folded in", body["from"])
	}
	to, _ := body["to"].([]any)
	if len(to) != 1 || to[0] != "user@example.com" {
		t.Errorf("to = %v", body["to"])
	}
	if body["subject"] != "Hi" || body["text"] != "text" {
		t.Errorf("subject/text = %v / %v", body["subject"], body["text"])
	}
	if raw, _ := json.Marshal(body); strings.Contains(string(raw), "re_test_key") {
		t.Error("the api key is in the request body")
	}
}

// The From identity is shared with SMTP on purpose, so switching provider does
// not ask the operator to retype their own address. A per-purpose override still
// wins, which is the one reason those keys are per-purpose at all.
func TestResendUsesThePerPurposeFromAddress(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()

	transport, err := Load(fakeSettings{
		SettingKeyProvider:        ProviderResend,
		SettingKeyResendAPIKey:    "re_test_key",
		settingKeyResendBaseURL:   srv.URL,
		"smtp.default.from_email": "noreply@example.com",
		"smtp.auth.from_email":    "accounts@example.com",
	}, "auth")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := transport.Send(Message{To: "user@example.com", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if body["from"] != "accounts@example.com" {
		t.Errorf("from = %v, want the auth-purpose address", body["from"])
	}
}

// Resend's own message names the real problem - an unverified domain, a
// malformed sender - and the status code alone does not. It ends up in a toast,
// so it has to survive.
func TestResendSurfacesTheProvidersError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"The example.com domain is not verified"}`))
	}))
	defer srv.Close()

	transport, err := Load(fakeSettings{
		SettingKeyProvider:        ProviderResend,
		SettingKeyResendAPIKey:    "re_test_key",
		settingKeyResendBaseURL:   srv.URL,
		"smtp.default.from_email": "noreply@example.com",
	}, "auth")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = transport.Send(Message{To: "user@example.com", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("a 422 was reported as success")
	}
	if !strings.Contains(err.Error(), "not verified") {
		t.Errorf("err = %v, want the provider's own message", err)
	}
}
