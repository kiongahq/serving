package serving

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

var digest = "sha256:" + strings.Repeat("c", 64)

func TestImagePolicy(t *testing.T) {
	allow, err := ParseAllowedPrefixes("ghcr.io/kiongahq/, registry.internal:5000/, ghcr.io/acme/churn, python")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, image   string
		requireDigest bool
		ok            bool
	}{
		{"untrusted hub image", "docker.io/evil/x:latest", false, false},
		{"untrusted hub short name", "evil/x", false, false},
		{"allowlisted digest", "ghcr.io/kiongahq/mlflow-serve@" + digest, true, true},
		{"allowlisted tag", "ghcr.io/kiongahq/mlflow-serve:3", false, true},
		{"tag rejected when digest required", "ghcr.io/kiongahq/mlflow-serve:3", true, false},
		{"prefix does not match a sibling org", "ghcr.io/kiongahq-evil/x@" + digest, false, false},
		{"registry prefix", "registry.internal:5000/team/model@" + digest, true, true},
		{"exact repository", "ghcr.io/acme/churn:4", false, true},
		{"exact repository is not a prefix", "ghcr.io/acme/churn-other:4", false, false},
		{"official image normalised", "docker.io/library/python:3.12", false, true},
		{"configured default always allowed", "mlaiops-mlflow", true, true},
		{"invalid reference", "ghcr.io/kiongahq/x?y=1", false, false},
		{"path traversal", "ghcr.io/kiongahq/../x", false, false},
		{"uppercase repository", "ghcr.io/kiongahq/X", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := ImagePolicy{AllowedPrefixes: allow, RequireDigest: tc.requireDigest}
			err := policy.check(tc.image, "mlaiops-mlflow")
			if (err == nil) != tc.ok {
				t.Fatalf("check(%q) = %v, want ok=%v", tc.image, err, tc.ok)
			}
		})
	}
}

func TestEmptyAllowlistAllowsOnlyDefaultImage(t *testing.T) {
	err := ImagePolicy{}.check("ghcr.io/kiongahq/mlflow-serve@"+digest, "mlaiops-mlflow")
	if !errors.Is(err, ErrImageNotAllowed) || !strings.Contains(err.Error(), "SERVE_IMAGE_ALLOWED_PREFIXES") {
		t.Fatalf("an empty allowlist must reject request images with a clear message: %v", err)
	}
	if err := (ImagePolicy{RequireDigest: true}).check("mlaiops-mlflow", "mlaiops-mlflow"); err != nil {
		t.Fatalf("the configured default image must stay usable: %v", err)
	}
}

func TestParseAllowedPrefixesRejectsGarbage(t *testing.T) {
	for _, value := range []string{"ghcr.io/Bad Org/", "https://ghcr.io/", "*"} {
		if _, err := ParseAllowedPrefixes(value); err == nil {
			t.Errorf("ParseAllowedPrefixes(%q) accepted an invalid entry", value)
		}
	}
	entries, err := ParseAllowedPrefixes(" , ghcr.io/, acme/")
	if err != nil || strings.Join(entries, ",") != "ghcr.io/,docker.io/acme/" {
		t.Fatalf("unexpected entries %v %v", entries, err)
	}
}

func TestDeployRejectsDisallowedImageBeforeTouchingDocker(t *testing.T) {
	e, server := newFakeEngine(t)
	manager := testManager(server.URL)
	manager.Images = ImagePolicy{AllowedPrefixes: []string{"ghcr.io/kiongahq/"}}
	_, err := manager.Deploy(context.Background(), "churn", "models:/churn/1", "docker.io/evil/x:latest")
	if !errors.Is(err, ErrImageNotAllowed) {
		t.Fatalf("expected ErrImageNotAllowed, got %v", err)
	}
	if len(e.calls) != 0 {
		t.Fatalf("a rejected image must not reach the engine: %v", e.calls)
	}
}

func TestDeployRequiresDigestWhenConfigured(t *testing.T) {
	_, server := newFakeEngine(t)
	manager := testManager(server.URL)
	manager.Images = ImagePolicy{AllowedPrefixes: []string{"ghcr.io/kiongahq/"}, RequireDigest: true}
	_, err := manager.Deploy(context.Background(), "churn", "models:/churn/1", "ghcr.io/kiongahq/mlflow-serve:3")
	if !errors.Is(err, ErrImageNotAllowed) || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a tag-only reference must be rejected when digests are required: %v", err)
	}
}

func TestDeployRunsResolvedImageID(t *testing.T) {
	e, server := newFakeEngine(t)
	ref := "ghcr.io/kiongahq/mlflow-serve@" + digest
	id := "sha256:" + strings.Repeat("d", 64)
	e.images[ref] = id
	manager := testManager(server.URL)
	manager.Images = ImagePolicy{AllowedPrefixes: []string{"ghcr.io/kiongahq/"}, RequireDigest: true}
	if _, err := manager.Deploy(context.Background(), "churn", "models:/churn/1", ref); err != nil {
		t.Fatal(err)
	}
	payload := e.created[servingName]
	labels := payload["Labels"].(map[string]any)
	if payload["Image"] != id || labels["mlaiops.image.id"] != id || labels["mlaiops.image"] != ref {
		t.Fatalf("container must run and record the resolved image ID: image %v labels %v", payload["Image"], labels)
	}
}

func TestDeployFailsWhenImageMissing(t *testing.T) {
	e, server := newFakeEngine(t)
	delete(e.images, "mlaiops-mlflow")
	_, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn/1", "")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("a missing image must fail clearly: %v", err)
	}
	if e.called("POST /containers/create") {
		t.Fatal("no container may be created for a missing image")
	}
}

func TestResolveImageRejectsInvalidID(t *testing.T) {
	e, server := newFakeEngine(t)
	e.on("GET /images/mlaiops-mlflow/json", http.StatusOK, `{"Id":"../../x"}`)
	if _, err := testManager(server.URL).resolveImage(context.Background(), "mlaiops-mlflow"); err == nil {
		t.Fatal("an invalid image ID must be rejected")
	}
}
