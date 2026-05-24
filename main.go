package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"syscall"

	"github.com/google/go-github/v70/github"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	githubOwner     = "DaxServer"
	githubRepo      = "curator"
	assetName       = "curator-server"
	downloadDest    = "/tmp/curator-server"
	actionsIssuer   = "https://token.actions.githubusercontent.com"
	signerWorkflow  = ".github/workflows/build.yml"
)

func main() {
	serverPath := downloadServer()
	slog.Info("starting curator-server", "path", serverPath)
	if err := syscall.Exec(serverPath, append([]string{assetName}, os.Args[1:]...), os.Environ()); err != nil {
		slog.Error("failed to exec curator-server", "error", err)
		os.Exit(1)
	}
}

func downloadServer() string {
	slog.Info("Downloading curator-server from GitHub releases")
	url, err := releaseAssetURL()
	if err != nil {
		slog.Error("Failed to fetch release info", "error", err)
		os.Exit(1)
	}
	slog.Info("Downloading", "url", url)
	if err := downloadFile(url, downloadDest); err != nil {
		slog.Error("Failed to download curator-server", "error", err)
		os.Exit(1)
	}
	slog.Info("Verifying attestation")
	if err := verifyAttestation(downloadDest); err != nil {
		slog.Error("Attestation verification failed", "error", err)
		os.Exit(1)
	}
	if err := os.Chmod(downloadDest, 0755); err != nil {
		slog.Error("Failed to chmod curator-server", "error", err)
		os.Exit(1)
	}
	return downloadDest
}

func newGitHubClient() *github.Client {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return github.NewClient(nil).WithAuthToken(token)
	}
	return github.NewClient(nil)
}

func releaseAssetURL() (string, error) {
	client := newGitHubClient()
	release, _, err := client.Repositories.GetLatestRelease(context.Background(), githubOwner, githubRepo)
	if err != nil {
		return "", err
	}
	for _, a := range release.Assets {
		if a.GetName() == assetName {
			return a.GetBrowserDownloadURL(), nil
		}
	}
	return "", fmt.Errorf("asset %q not found in latest release", assetName)
}

func verifyAttestation(binaryPath string) error {
	digest, err := computeSHA256(binaryPath)
	if err != nil {
		return fmt.Errorf("computing digest: %w", err)
	}

	sigstoreBundles, err := fetchGitHubAttestations(digest)
	if err != nil {
		return fmt.Errorf("fetching attestations: %w", err)
	}
	if len(sigstoreBundles) == 0 {
		return fmt.Errorf("no attestations found for binary")
	}

	trustedRoot, err := root.FetchTrustedRoot()
	if err != nil {
		return fmt.Errorf("fetching trusted root: %w", err)
	}

	verifier, err := verify.NewVerifier(trustedRoot,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("creating verifier: %w", err)
	}

	digestBytes, err := hex.DecodeString(digest)
	if err != nil {
		return fmt.Errorf("decoding digest: %w", err)
	}

	sanRegex := fmt.Sprintf("^https://github\\.com/%s/%s/%s@", githubOwner, githubRepo, signerWorkflow)
	certID, err := verify.NewShortCertificateIdentity(actionsIssuer, "", "", sanRegex)
	if err != nil {
		return fmt.Errorf("creating cert identity: %w", err)
	}

	policy := verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digestBytes),
		verify.WithCertificateIdentity(certID),
	)

	var lastErr error
	for _, b := range sigstoreBundles {
		if _, err := verifier.Verify(b, policy); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return fmt.Errorf("no valid attestation found: %w", lastErr)
}

func computeSHA256(path string) (string, error) {
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

func fetchGitHubAttestations(digest string) ([]*bundle.Bundle, error) {
	client := newGitHubClient()
	result, _, err := client.Repositories.ListAttestations(
		context.Background(), githubOwner, githubRepo, "sha256:"+digest, nil,
	)
	if err != nil {
		return nil, err
	}

	var bundles []*bundle.Bundle
	for _, a := range result.Attestations {
		var pb protobundle.Bundle
		if err := protojson.Unmarshal(a.Bundle, &pb); err != nil {
			continue
		}
		b, err := bundle.NewBundle(&pb)
		if err != nil {
			continue
		}
		bundles = append(bundles, b)
	}
	return bundles, nil
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
