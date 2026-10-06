package runtimeconfig

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretFilesAndConflicts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte("private-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OIDC_CLIENT_SECRET", "")
	t.Setenv("OIDC_CLIENT_SECRET_FILE", file)
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OIDC_CLIENT_SECRET") != "private-value" {
		t.Fatal("secret not loaded")
	}
	if err := Load(); err == nil {
		t.Fatal("conflicting inputs accepted")
	}
	t.Setenv("OIDC_CLIENT_SECRET", "")
	t.Setenv("OIDC_CLIENT_SECRET_FILE", file+"missing")
	if err := Load(); err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("unsafe error")
	}
}
func TestConfigRejectsSecrets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(file, []byte(`{"OIDC_CLIENT_SECRET":"do-not-store-here"}`), 0600)
	t.Setenv("KIONGA_CONFIG_FILE", file)
	if err := Load(); err == nil || strings.Contains(err.Error(), "do-not-store-here") {
		t.Fatal("secret config accepted/leaked")
	}
}

func TestConfigAllowsTokenEndpoint(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"OIDC_TOKEN_URL":"https://id.company.test/token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIONGA_CONFIG_FILE", file)
	previous, present := os.LookupEnv("OIDC_TOKEN_URL")
	_ = os.Unsetenv("OIDC_TOKEN_URL")
	t.Cleanup(func() {
		if present {
			_ = os.Setenv("OIDC_TOKEN_URL", previous)
		} else {
			_ = os.Unsetenv("OIDC_TOKEN_URL")
		}
	})
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OIDC_TOKEN_URL") != "https://id.company.test/token" {
		t.Fatal("endpoint not loaded")
	}
}
func TestProductionFailClosed(t *testing.T) {
	t.Setenv("KIONGA_ENVIRONMENT", "production")
	for _, key := range []string{"OIDC_ISSUER", "OIDC_JWKS_URL", "OIDC_AUTH_URL", "OIDC_TOKEN_URL"} {
		t.Setenv(key, "https://id.company.test/path")
	}
	t.Setenv("OIDC_AUDIENCE", "kionga")
	t.Setenv("OIDC_CLIENT_ID", "kionga")
	t.Setenv("OIDC_CLIENT_SECRET", strings.Repeat("s", 32))
	t.Setenv("MLAIOPS_INTERNAL_TOKEN", strings.Repeat("t", 32))
	t.Setenv("KIONGA_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("DATABASE_URL", "postgres://user:pass@db.company.test/db?sslmode=verify-full")
	t.Setenv("MLAIOPS_ALLOWED_ORIGIN", "https://app.company.test")
	t.Setenv("OIDC_REDIRECT_URL", "https://app.company.test/auth/callback")
	if err := ValidateGateway(); err != nil {
		t.Fatal(err)
	}
	for _, change := range [][2]string{{"OIDC_ISSUER", ""}, {"OIDC_TOKEN_URL", "http://id.company.test/token"}, {"DATABASE_URL", "postgres://db/db?sslmode=disable"}, {"OIDC_REDIRECT_URL", "https://evil.test/auth/callback"}, {"MLAIOPS_INTERNAL_TOKEN", "kionga-local-service-token"}, {"KIONGA_CREDENTIAL_KEY", "invalid"}} {
		t.Run(change[0], func(t *testing.T) {
			t.Setenv(change[0], change[1])
			if ValidateGateway() == nil {
				t.Fatal("unsafe production config accepted")
			}
		})
	}
}

func TestProductionWorkspaceDomains(t *testing.T) {
	t.Setenv("KIONGA_ENVIRONMENT", "production")
	for _, key := range []string{"OIDC_ISSUER", "OIDC_JWKS_URL", "OIDC_AUTH_URL", "OIDC_TOKEN_URL"} {
		t.Setenv(key, "https://id.example.test/path")
	}
	t.Setenv("OIDC_AUDIENCE", "kionga")
	t.Setenv("OIDC_CLIENT_ID", "kionga")
	t.Setenv("OIDC_CLIENT_SECRET", strings.Repeat("s", 32))
	t.Setenv("MLAIOPS_INTERNAL_TOKEN", strings.Repeat("t", 32))
	t.Setenv("KIONGA_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("DATABASE_URL", "postgres://user:pass@db.example.test/db?sslmode=verify-full")
	t.Setenv("MLAIOPS_ALLOWED_ORIGIN", "https://console.example.test")
	t.Setenv("OIDC_REDIRECT_URL", "https://console.example.test/auth/callback")
	t.Setenv("KIONGA_WORKSPACE_NAMESPACE", "kionga-workloads")
	t.Setenv("KIONGA_WORKSPACE_BASE_DOMAIN", "workspaces.example.test")
	if err := ValidateGateway(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIONGA_WORKSPACE_BASE_DOMAIN", "workspaces.other.test")
	if ValidateGateway() == nil {
		t.Fatal("cross-site workspace configuration accepted")
	}
}
