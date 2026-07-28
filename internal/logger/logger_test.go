package logger

import (
	"strings"
	"testing"
)

func TestRedactSensitive(t *testing.T) {
	tests := []struct {
		input  string
		secret string
	}{
		{"auth_token=topsecret", "topsecret"},
		{"Authorization: topsecret", "topsecret"},
		{"Cookie: SESSDATA=topsecret", "topsecret"},
		{"Bearer topsecret", "topsecret"},
	}
	for _, tt := range tests {
		got := redactSensitive(tt.input)
		if got == tt.input || strings.Contains(got, tt.secret) {
			t.Fatalf("secret was not redacted: input=%q output=%q", tt.input, got)
		}
	}
}
