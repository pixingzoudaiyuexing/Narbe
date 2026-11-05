package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRESTProviderErrorsRetainSafeOfficialReason(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"linode", `{"errors":[{"field":"status","reason":"The Linode is currently being migrated."}]}`, "The Linode is currently being migrated."},
		{"digitalocean", `{"id":"conflict","message":"Droplet is currently locked by another action."}`, "Droplet is currently locked by another action."},
		{"azure", `{"error":{"code":"OperationNotAllowed","message":"The VM is already deallocated."}}`, "OperationNotAllowed: The VM is already deallocated."},
		{"multiple", `{"errors":[{"reason":"Disk is busy."},{"reason":"Try again after migration completes."}]}`, "Disk is busy.; Try again after migration completes."},
		{"controls", "{\"message\":\"The VM\\u0000 is\\n busy.\\u202e\"}", "The VM is busy."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rest{base: "https://api.example.test", token: "fixture-account-token", h: client(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer fixture-account-token" {
					t.Fatal("request contract changed")
				}
				return response(http.StatusConflict, tc.body), nil
			})}
			err := r.call(context.Background(), "POST", "/action", map[string]string{"action": "shutdown"}, nil)
			var ce *Error
			if !errors.As(err, &ce) || ce.Status != http.StatusConflict || ce.Uncertain || ce.Code != tc.reason {
				t.Fatalf("official reason lost: %#v", err)
			}
		})
	}
}

func TestRESTProviderErrorsNeverEchoRequestSecrets(t *testing.T) {
	secret := "PrIvAtE-fixture-password-1!"
	script := "#!/bin/sh\necho sensitive-startup-marker\n"
	body := map[string]any{
		"password":         secret,
		"metadata":         map[string]string{"user_data": base64.StdEncoding.EncodeToString([]byte(script))},
		"stackscript_data": map[string]string{"application_key": "sensitive-udf-marker"},
		"authorized_keys":  []string{"ssh-ed25519 sensitive-public-key-marker fixture"},
	}
	for _, reason := range []string{
		"The submitted value was " + secret,
		"The submitted value was " + strings.ToLower(secret),
		"The submitted value was " + url.QueryEscape(secret),
		"The submitted value was " + base64.StdEncoding.EncodeToString([]byte(secret)),
		"The submitted value was " + script,
		"The submitted value was " + base64.StdEncoding.EncodeToString([]byte(script)),
		"The submitted value was sensitive-udf-marker",
		"The submitted value was ssh-ed25519 sensitive-public-key-marker fixture",
		"The submitted value was fixture-account-token",
		"quota limit exceeded token=unrelated-secret-not-in-request",
		"Authorization: Bearer unrelated-secret-not-in-request",
		"private raw secret",
		"access_token=unrelated-secret-not-in-request",
		"rootPass=unrelated-secret-not-in-request",
		"The upstream response was <html>unrelated-secret-not-in-request</html>",
	} {
		raw, _ := json.Marshal(map[string]any{"errors": []any{map[string]string{"reason": reason}}})
		r := rest{base: "https://api.example.test", token: "fixture-account-token", h: client(func(*http.Request) (*http.Response, error) {
			return response(http.StatusBadRequest, string(raw)), nil
		})}
		err := r.call(context.Background(), "POST", "/action", body, nil)
		if err == nil {
			t.Fatal("missing provider error")
		}
		for _, forbidden := range []string{secret, strings.ToLower(secret), url.QueryEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), "sensitive-startup-marker", base64.StdEncoding.EncodeToString([]byte(script)), "sensitive-udf-marker", "sensitive-public-key-marker", "fixture-account-token", "unrelated-secret-not-in-request", "private raw secret"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("provider error leaked request data: %q", forbidden)
			}
		}
	}
}

func TestRESTProviderErrorFallbackAndUncertaintyStayBounded(t *testing.T) {
	for _, status := range []int{400, 408, 409, 422, 429, 500, 503} {
		for _, method := range []string{"GET", "POST"} {
			for _, body := range []string{
				`{"unexpected":{"message":"never-display-unrecognized-field"}}`,
				`{"message":{"value":"never-display-object-as-text"}}`,
				`not JSON secret=never-display-raw-body`,
				`{"message":"` + strings.Repeat("safe-reason ", 200) + `"}`,
			} {
				r := rest{base: "https://api.example.test", h: client(func(*http.Request) (*http.Response, error) {
					return response(status, body), nil
				})}
				err := r.call(context.Background(), method, "/action", nil, nil)
				var ce *Error
				if !errors.As(err, &ce) || ce.Status != status || ce.Uncertain != (method != "GET" && (status >= 500 || status == 408)) {
					t.Fatal("provider status/uncertainty changed", status, method, err)
				}
				if strings.Contains(err.Error(), "never-display") || utf8.RuneCountInString(ce.Code) > 400 {
					t.Fatal("unrecognized or excessive response escaped error boundary", err)
				}
			}
		}
	}
}

func FuzzRESTProviderErrorBoundary(f *testing.F) {
	for _, seed := range []string{
		`{"errors":[{"reason":"The disk is busy."}]}`,
		`{"message":"sensitive-fuzz-password"}`,
		`{"message":"Authorization: Bearer sensitive-fuzz-token"}`,
		`{"message":"to\u200bken=do-not-display"}`,
		`{"message":{"nested":"never-display"}}`,
		`<script>sensitive-fuzz-password</script>`,
		"\x00\xff",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		r := rest{base: "https://api.example.test", token: "sensitive-fuzz-token", h: client(func(*http.Request) (*http.Response, error) {
			return response(http.StatusServiceUnavailable, body), nil
		})}
		err := r.call(context.Background(), "POST", "/action", map[string]string{"password": "sensitive-fuzz-password"}, nil)
		var ce *Error
		if !errors.As(err, &ce) || !ce.Uncertain || ce.Status != http.StatusServiceUnavailable {
			t.Fatal("error classification changed", err)
		}
		if strings.Contains(err.Error(), "sensitive-fuzz-password") || strings.Contains(err.Error(), "sensitive-fuzz-token") || utf8.RuneCountInString(ce.Code) > 400 {
			t.Fatal("error boundary exposed credentials or an unbounded response")
		}
	})
}
