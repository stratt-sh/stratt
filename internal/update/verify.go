package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// AttestationPolicy describes the trust anchor pin embedded in the
// stratt binary (R4.4).  Self-update only accepts attestations whose
// certificate was issued for a workflow path under SubjectRepository.
type AttestationPolicy struct {
	// SubjectRepository is the GitHub repo that owns the trusted release
	// workflow, e.g. "stratt-sh/stratt".
	SubjectRepository string

	// WorkflowRef is the Git ref of the workflow file used to gate
	// signing, e.g. "refs/heads/main" or "refs/tags/v*".  Empty means
	// "any ref under the trusted repo."
	WorkflowRef string
}

// DefaultPolicy is the production trust anchor for stratt's own release
// pipeline.  Other instances can build their own policy at compile time
// if they're forking stratt for an internal-only deployment.
var DefaultPolicy = AttestationPolicy{
	SubjectRepository: "stratt-sh/stratt",
}

// ErrAttestationMissing — no attestation could be found for an artifact.
var ErrAttestationMissing = errors.New("no attestation found")

// FetchAttestationBundle downloads the sigstore bundle for an artifact
// from GitHub's attestations API.  digest is the hex SHA-256 of the
// artifact (no `sha256:` prefix).
func FetchAttestationBundle(ctx context.Context, client *http.Client, repo, hexDigest string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/attestations/sha256:%s", repo, hexDigest)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrAttestationMissing
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch attestation: status %d: %s", resp.StatusCode, string(body))
	}

	// The response is wrapped: { "attestations": [ { "bundle": {...} } ] }
	// We pull the first bundle out.
	var wrapper struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode attestations response: %w", err)
	}
	if len(wrapper.Attestations) == 0 || len(wrapper.Attestations[0].Bundle) == 0 {
		return nil, ErrAttestationMissing
	}
	return wrapper.Attestations[0].Bundle, nil
}

// VerifyArtifact verifies that the artifact at path was signed by a
// trusted workflow in policy.SubjectRepository, per the embedded
// attestation bundle.  This is the central trust check for R4.3.
//
// Network access is required: sigstore-go fetches Sigstore's TUF root
// to validate the signing certificate chain.  The trusted root is
// cached under ~/.sigstore by the library.
func VerifyArtifact(ctx context.Context, artifactPath string, bundleJSON []byte, policy AttestationPolicy) error {
	// Open and hash the artifact.
	f, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	digest := h.Sum(nil)

	// Parse the bundle.  UnmarshalJSON allocates the inner protobuf
	// bundle and validates it, so we start from an empty Bundle rather
	// than NewBundle(nil) (which would dereference a nil bundle in its
	// own validate() and panic).
	b := &bundle.Bundle{}
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("parse bundle: %w", err)
	}

	// Fetch trusted root from Sigstore's TUF repo.
	trusted, err := root.FetchTrustedRoot()
	if err != nil {
		return fmt.Errorf("fetch trusted root: %w", err)
	}

	verifier, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("init verifier: %w", err)
	}

	// Build the policy.  The artifact identity (sha256 digest) and the
	// expected certificate identity (repo / workflow path) are the
	// pin points.
	identityPolicy, err := buildIdentityPolicy(policy)
	if err != nil {
		return fmt.Errorf("build identity policy: %w", err)
	}
	artifactPolicy := verify.WithArtifactDigest("sha256", digest)
	pb := verify.NewPolicy(artifactPolicy, identityPolicy)

	if _, err := verifier.Verify(b, pb); err != nil {
		return fmt.Errorf("attestation verification failed: %w", err)
	}
	return nil
}

// buildIdentityPolicy constructs the certificate-subject policy from the
// configured AttestationPolicy.  The pattern matches GitHub Actions OIDC
// subjects, which look like:
//
//	https://github.com/<owner>/<repo>/.github/workflows/<file>@<ref>
//
// We pin the repo prefix; ref-pinning is optional per R4.4.
func buildIdentityPolicy(p AttestationPolicy) (verify.PolicyOption, error) {
	if p.SubjectRepository == "" {
		return nil, errors.New("AttestationPolicy.SubjectRepository must be set")
	}
	subjectPrefix := fmt.Sprintf("https://github.com/%s/.github/workflows/", p.SubjectRepository)
	issuer := "https://token.actions.githubusercontent.com"
	id, err := verify.NewShortCertificateIdentity(
		issuer,
		"",            // SAN type: empty → any
		"",            // SAN value: empty → any
		subjectPrefix, // SAN value prefix
	)
	if err != nil {
		return nil, err
	}
	return verify.WithCertificateIdentity(id), nil
}

// FileSHA256 returns the hex SHA-256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
