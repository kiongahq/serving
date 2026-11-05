package serving

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// ErrImageNotAllowed reports a serving_image outside the operator's policy.
var ErrImageNotAllowed = errors.New("serving image is not allowed")

// ImagePolicy restricts the serving_image a request may choose. The manager's
// own SERVE_IMAGE is operator configuration and is always allowed.
type ImagePolicy struct {
	// AllowedPrefixes lists repositories a request may use. An entry ending in
	// "/" allows every repository below it ("ghcr.io/acme/"); any other entry
	// allows exactly that repository ("ghcr.io/acme/churn"). Empty allows only
	// the configured SERVE_IMAGE.
	AllowedPrefixes []string
	// RequireDigest rejects request images that are not pinned with
	// @sha256:<digest>. Production always sets it.
	RequireDigest bool
}

const (
	component  = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`
	domainPart = `(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])`
	domain     = domainPart + `(?:\.` + domainPart + `)*(?::[0-9]+)?`
)

// referencePattern is the OCI distribution reference grammar: an optional
// registry, a repository path, an optional tag and an optional sha256 digest.
var (
	referencePattern = regexp.MustCompile(`^(?:(` + domain + `)/)?(` + component + `(?:/` + component + `)*)(?::([\w][\w.-]{0,127}))?(?:@(sha256:[a-f0-9]{64}))?$`)
	repositoryEntry  = regexp.MustCompile(`^(?:(` + domain + `)/)?(` + component + `(?:/` + component + `)*)/?$`)
	registryPattern  = regexp.MustCompile(`^` + domain + `$`)
	imageIDPattern   = regexp.MustCompile(`^(?:sha256:)?[a-f0-9]{64}$`)
)

type imageReference struct {
	repository string // canonical, e.g. docker.io/library/python
	digest     string
}

// canonicalRepository applies Docker's defaults: no registry means docker.io,
// and a single-component Docker Hub name lives under library/.
func canonicalRepository(registry, path string) string {
	if registry != "" && !strings.ContainsAny(registry, ".:") && registry != "localhost" {
		// "acme/churn": the first component is a namespace, not a registry.
		path = registry + "/" + path
		registry = ""
	}
	if registry == "" {
		registry = "docker.io"
		if !strings.Contains(path, "/") {
			path = "library/" + path
		}
	}
	return registry + "/" + path
}

func parseReference(value string) (imageReference, error) {
	match := referencePattern.FindStringSubmatch(value)
	if match == nil || len(value) > 512 {
		return imageReference{}, fmt.Errorf("serving image %q is not a valid image reference", value)
	}
	return imageReference{repository: canonicalRepository(match[1], match[2]), digest: match[4]}, nil
}

// ParseAllowedPrefixes parses SERVE_IMAGE_ALLOWED_PREFIXES, a comma-separated
// list of registries or repositories.
func ParseAllowedPrefixes(value string) ([]string, error) {
	var entries []string
	for _, raw := range strings.Split(value, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		prefix := strings.HasSuffix(entry, "/")
		trimmed := strings.TrimSuffix(entry, "/")
		// A bare registry with a trailing slash: "ghcr.io/", "registry.internal:5000/".
		if prefix && !strings.Contains(trimmed, "/") && registryPattern.MatchString(trimmed) && (strings.ContainsAny(trimmed, ".:") || trimmed == "localhost") {
			entries = append(entries, trimmed+"/")
			continue
		}
		if match := repositoryEntry.FindStringSubmatch(entry); match != nil {
			path := strings.TrimSuffix(match[2], "/")
			canonical := canonicalRepository(match[1], path)
			if prefix {
				canonical += "/"
				if match[1] == "" && !strings.Contains(path, "/") {
					// "acme/" is a Docker Hub namespace, not library/acme.
					canonical = "docker.io/" + path + "/"
				}
			}
			entries = append(entries, canonical)
			continue
		}
		return nil, fmt.Errorf("SERVE_IMAGE_ALLOWED_PREFIXES entry %q is not a registry or repository", entry)
	}
	return entries, nil
}

func (p ImagePolicy) allows(repository string) bool {
	for _, entry := range p.AllowedPrefixes {
		if strings.HasSuffix(entry, "/") && strings.HasPrefix(repository, entry) || entry == repository {
			return true
		}
	}
	return false
}

// check applies the policy to a request's serving_image. configured is the
// manager's SERVE_IMAGE, which a request may always name explicitly.
func (p ImagePolicy) check(image, configured string) error {
	ref, err := parseReference(image)
	if err != nil {
		return err
	}
	if image == configured {
		return nil
	}
	if !p.allows(ref.repository) {
		if len(p.AllowedPrefixes) == 0 {
			return fmt.Errorf("%w: %q; only the default serving image is allowed until the operator sets SERVE_IMAGE_ALLOWED_PREFIXES", ErrImageNotAllowed, image)
		}
		return fmt.Errorf("%w: %q is outside SERVE_IMAGE_ALLOWED_PREFIXES (%s)", ErrImageNotAllowed, image, strings.Join(p.AllowedPrefixes, ", "))
	}
	if p.RequireDigest && ref.digest == "" {
		return fmt.Errorf("%w: %q must be pinned by digest (name@sha256:...)", ErrImageNotAllowed, image)
	}
	return nil
}

// resolveImage returns the engine's content-addressed ID for image, so the
// container runs exactly the image that was checked even if a tag moves.
func (m *Manager) resolveImage(ctx context.Context, image string) (string, error) {
	var inspected struct {
		ID string `json:"Id"`
	}
	// image matched referencePattern, so it holds only path-safe characters.
	if _, err := m.do(ctx, http.MethodGet, "/images/"+image+"/json", nil, &inspected); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("serving image %q is not present on the docker engine: %w", image, err)
		}
		return "", fmt.Errorf("inspect serving image: %w", err)
	}
	if !imageIDPattern.MatchString(inspected.ID) {
		return "", fmt.Errorf("docker engine returned an invalid ID %q for image %q", inspected.ID, image)
	}
	if !strings.HasPrefix(inspected.ID, "sha256:") {
		return "sha256:" + inspected.ID, nil
	}
	return inspected.ID, nil
}
