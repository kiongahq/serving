// Package runtimeconfig reads non-secret JSON configuration and mounted secrets.
package runtimeconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

var SecretNames = []string{"DATABASE_URL", "OIDC_CLIENT_SECRET", "MLAIOPS_INTERNAL_TOKEN", "KIONGA_CREDENTIAL_KEY", "S3_ACCESS_KEY", "S3_SECRET_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "REDIS_URL", "KAFKA_REST_TOKEN", "KFP_TOKEN", "MLFLOW_TOKEN", "OPENFAAS_PASSWORD", "LANGFUSE_SECRET_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "LLM_UPSTREAM_API_KEY", "KIONGA_JUPYTER_TOKEN", "KIONGA_IDE_PASSWORD", "TRACE_SINK_TOKEN"}
var keyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if len(data) > 65536 {
		return nil, fmt.Errorf("file exceeds 64 KiB")
	}
	return data, err
}

func Load() error {
	secrets := map[string]bool{}
	for _, key := range SecretNames {
		secrets[key] = true
	}
	if path := os.Getenv("KIONGA_CONFIG_FILE"); path != "" {
		raw, err := readFile(path)
		if err != nil {
			return fmt.Errorf("cannot read KIONGA_CONFIG_FILE")
		}
		var values map[string]string
		if json.Unmarshal(raw, &values) != nil {
			return fmt.Errorf("KIONGA_CONFIG_FILE must be a JSON object of string values")
		}
		for key, value := range values {
			if !keyPattern.MatchString(key) || secrets[key] || strings.HasSuffix(key, "_FILE") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "SECRET") || (strings.Contains(key, "TOKEN") && key != "OIDC_TOKEN_URL") || strings.Contains(key, "API_KEY") {
				return fmt.Errorf("config key %s is invalid or must be supplied separately as a secret", key)
			}
			if _, present := os.LookupEnv(key); !present {
				if err := os.Setenv(key, value); err != nil {
					return fmt.Errorf("could not load %s", key)
				}
			}
		}
	}
	for _, key := range SecretNames {
		if path := os.Getenv(key + "_FILE"); path != "" {
			if os.Getenv(key) != "" {
				return fmt.Errorf("set only one of %s and %s_FILE", key, key)
			}
			data, err := readFile(path)
			if err != nil {
				return fmt.Errorf("cannot read %s_FILE", key)
			}
			value := strings.TrimRight(string(data), "\r\n")
			if value == "" {
				return fmt.Errorf("%s_FILE is empty", key)
			}
			if err = os.Setenv(key, value); err != nil {
				return fmt.Errorf("could not load %s_FILE", key)
			}
		}
	}
	return nil
}

func ValidateGateway() error {
	if os.Getenv("KIONGA_ENVIRONMENT") != "production" {
		return nil
	}
	for _, key := range []string{"DATABASE_URL", "OIDC_ISSUER", "OIDC_JWKS_URL", "OIDC_AUDIENCE", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_AUTH_URL", "OIDC_TOKEN_URL", "OIDC_REDIRECT_URL", "MLAIOPS_INTERNAL_TOKEN", "KIONGA_CREDENTIAL_KEY", "MLAIOPS_ALLOWED_ORIGIN"} {
		if os.Getenv(key) == "" {
			return fmt.Errorf("production requires %s", key)
		}
	}
	for _, key := range []string{"OIDC_ISSUER", "OIDC_JWKS_URL", "OIDC_AUTH_URL", "OIDC_TOKEN_URL", "OIDC_REDIRECT_URL", "MLAIOPS_ALLOWED_ORIGIN"} {
		u, err := url.Parse(os.Getenv(key))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("production requires an HTTPS URL for %s", key)
		}
	}
	origin, _ := url.Parse(os.Getenv("MLAIOPS_ALLOWED_ORIGIN"))
	redirect, _ := url.Parse(os.Getenv("OIDC_REDIRECT_URL"))
	if origin.Host != redirect.Host || redirect.Path != "/auth/callback" {
		return fmt.Errorf("OIDC_REDIRECT_URL must match the public origin and /auth/callback")
	}
	if base := os.Getenv("KIONGA_WORKSPACE_BASE_DOMAIN"); base != "" {
		if os.Getenv("KIONGA_TRUSTED_WORKSPACE_PROXY") == "true" || strings.ContainsAny(base, ":/ ") || !strings.Contains(base, ".") || strings.EqualFold(base, origin.Hostname()) || os.Getenv("KIONGA_WORKSPACE_NAMESPACE") == "" {
			return fmt.Errorf("isolated workspaces require a distinct DNS base domain, Kubernetes discovery and the trusted proxy disabled")
		}
		platformSite, platformErr := publicsuffix.EffectiveTLDPlusOne(origin.Hostname())
		workspaceSite, workspaceErr := publicsuffix.EffectiveTLDPlusOne(base)
		if platformErr != nil || workspaceErr != nil || !strings.EqualFold(platformSite, workspaceSite) {
			return fmt.Errorf("workspace and console hosts must share a registrable site for host-only workspace cookies")
		}
	}
	db, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil || (db.Scheme != "postgres" && db.Scheme != "postgresql") || db.Host == "" || db.Query().Get("sslmode") != "verify-full" {
		return fmt.Errorf("production DATABASE_URL must use PostgreSQL with sslmode=verify-full")
	}
	for _, key := range []string{"OIDC_CLIENT_SECRET", "MLAIOPS_INTERNAL_TOKEN"} {
		value := os.Getenv(key)
		if len(value) < 24 || strings.Contains(strings.ToLower(value), "local") || strings.Contains(strings.ToLower(value), "change-me") {
			return fmt.Errorf("production %s must be a strong non-development credential", key)
		}
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("KIONGA_CREDENTIAL_KEY"))
	if err != nil || len(key) != 32 {
		return fmt.Errorf("KIONGA_CREDENTIAL_KEY must encode 32 bytes")
	}
	return nil
}
