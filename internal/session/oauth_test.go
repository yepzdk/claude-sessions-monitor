package session

import "testing"

// Credentials that parse but carry no access token used to count as a token.
// csm then sent "Bearer" with nothing after it, and the quota panel reported
// the API's rejection instead of the sign-in that would fix it.
func TestParseCredentialsRejectsCredentialsWithNoUsableToken(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		token string // empty means the parse must fail
	}{
		{"a real token", `{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc"}}`, "sk-ant-oat-abc"},
		{"the access token is empty", `{"claudeAiOauth":{"accessToken":""}}`, ""},
		{"no oauth section at all", `{"somethingElse":{"accessToken":"abc"}}`, ""},
		{"not JSON", `not json`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := parseCredentials([]byte(tt.data))
			if tt.token == "" {
				if err == nil {
					t.Fatalf("accepted %s as a token", tt.name)
				}
				if token != nil {
					t.Errorf("returned a token alongside an error: %+v", token)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCredentials: %v", err)
			}
			if token.AccessToken != tt.token {
				t.Errorf("AccessToken = %q, want %q", token.AccessToken, tt.token)
			}
		})
	}
}
