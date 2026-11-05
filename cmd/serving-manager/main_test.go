package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
		{"secret-token", "Basic secret-token", false},
		{"secret-token", "bearer secret-token", false},
		{"secret-token", "Bearer secret-token ", false},
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

// TestMainRefusesToStartWithoutToken runs main in a child process: with no
// MLAIOPS_INTERNAL_TOKEN it must exit non-zero before it listens.
func TestMainRefusesToStartWithoutToken(t *testing.T) {
	if os.Getenv("SERVING_MAIN_CHILD") == "1" {
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainRefusesToStartWithoutToken$") // #nosec G204 G702 -- re-executes this test binary
	cmd.Env = append(os.Environ(), "SERVING_MAIN_CHILD=1", "MLAIOPS_INTERNAL_TOKEN=", "MLAIOPS_INTERNAL_TOKEN_FILE=", "KIONGA_CONFIG_FILE=", "PORT=0")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("serving-manager kept running without a token: %s", output)
	}
	if err == nil {
		t.Fatalf("serving-manager started without a token: %s", output)
	}
	if !strings.Contains(string(output), "requires MLAIOPS_INTERNAL_TOKEN") {
		t.Fatalf("missing a clear startup error: %s", output)
	}
}

func TestImagePolicyFromEnvironment(t *testing.T) {
	t.Setenv("KIONGA_ENVIRONMENT", "")
	t.Setenv("SERVE_IMAGE_ALLOWED_PREFIXES", "ghcr.io/kiongahq/")
	t.Setenv("SERVE_IMAGE_REQUIRE_DIGEST", "")
	policy, err := imagePolicy()
	if err != nil || policy.RequireDigest || strings.Join(policy.AllowedPrefixes, ",") != "ghcr.io/kiongahq/" {
		t.Fatalf("unexpected development policy %+v %v", policy, err)
	}
	t.Setenv("KIONGA_ENVIRONMENT", "production")
	if policy, err = imagePolicy(); err != nil || !policy.RequireDigest {
		t.Fatalf("production must require digests: %+v %v", policy, err)
	}
	t.Setenv("SERVE_IMAGE_REQUIRE_DIGEST", "false")
	if _, err = imagePolicy(); err == nil {
		t.Fatal("production must refuse SERVE_IMAGE_REQUIRE_DIGEST=false")
	}
	t.Setenv("KIONGA_ENVIRONMENT", "")
	t.Setenv("SERVE_IMAGE_REQUIRE_DIGEST", "maybe")
	if _, err = imagePolicy(); err == nil {
		t.Fatal("an invalid boolean must be rejected")
	}
	t.Setenv("SERVE_IMAGE_REQUIRE_DIGEST", "")
	t.Setenv("SERVE_IMAGE_ALLOWED_PREFIXES", "https://evil/")
	if _, err = imagePolicy(); err == nil {
		t.Fatal("an invalid allowlist must be rejected")
	}
}
