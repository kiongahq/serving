package main

import (
	"net/http/httptest"
	"testing"
)

func TestAuthorizedFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		token, header string
		want          bool
	}{
		{"", "", false},
		{"", "Bearer ", false},
		{"secret-token", "", false},
		{"secret-token", "Bearer wrong", false},
		{"secret-token", "secret-token", false},
		{"secret-token", "Bearer secret-token", true},
	} {
		t.Setenv("MLAIOPS_INTERNAL_TOKEN", tc.token)
		r := httptest.NewRequest("GET", "/deployments", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		if got := authorized(r); got != tc.want {
			t.Errorf("token %q header %q: authorized = %v, want %v", tc.token, tc.header, got, tc.want)
		}
	}
}
