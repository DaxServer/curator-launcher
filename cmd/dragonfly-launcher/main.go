package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/go-github/v70/github"
)

const (
	githubOwner  = "dragonflydb"
	githubRepo   = "dragonfly"
	downloadDest = "/tmp/dragonfly"

	cgroupV2MemFile = "/sys/fs/cgroup/memory.max"
	cgroupV1MemFile = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	// cgroups v1 reports this sentinel when there is no limit
	cgroupV1Unlimited = int64(9223372036854771712)
)

func main() {
	assetName, err := archAssetName()
	if err != nil {
		slog.Error("unsupported architecture", "error", err)
		os.Exit(1)
	}

	if err := downloadBinary(assetName); err != nil {
		slog.Error("failed to download dragonfly", "error", err)
		os.Exit(1)
	}

	if err := os.Chmod(downloadDest, 0755); err != nil {
		slog.Error("failed to chmod dragonfly", "error", err)
		os.Exit(1)
	}

	args := buildArgs()
	slog.Info("starting dragonfly", "args", args)
	if err := syscall.Exec(downloadDest, append([]string{"dragonfly"}, args...), os.Environ()); err != nil {
		slog.Error("failed to exec dragonfly", "error", err)
		os.Exit(1)
	}
}

func archAssetName() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "dragonfly-x86_64.tar.gz", nil
	case "arm64":
		return "dragonfly-aarch64.tar.gz", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

func newGitHubClient() *github.Client {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return github.NewClient(nil).WithAuthToken(token)
	}
	return github.NewClient(nil)
}

func releaseAssetURL(assetName string) (string, error) {
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

func downloadBinary(assetName string) error {
	slog.Info("fetching latest dragonfly release", "asset", assetName)
	url, err := releaseAssetURL(assetName)
	if err != nil {
		return fmt.Errorf("fetching release asset URL: %w", err)
	}

	slog.Info("downloading", "url", url)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	return extractBinary(resp.Body)
}

func extractBinary(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		f, err := os.Create(downloadDest)
		if err != nil {
			return fmt.Errorf("creating destination file: %w", err)
		}
		defer f.Close()
		if _, err := io.Copy(f, tr); err != nil {
			return fmt.Errorf("extracting binary: %w", err)
		}
		return nil
	}
	return fmt.Errorf("dragonfly binary not found in archive")
}

func buildArgs() []string {
	var args []string
	args = append(args, "--logtostderr")
	if maxmem := detectMaxMemory(); maxmem > 0 {
		slog.Info("setting maxmemory from cgroup limit", "bytes", maxmem)
		args = append(args, fmt.Sprintf("--maxmemory=%d", maxmem))
	}
	args = append(args, os.Args[1:]...)
	return args
}

func detectMaxMemory() int64 {
	if v := readCgroupV2Memory(); v > 0 {
		return v
	}
	return readCgroupV1Memory()
}

func readCgroupV2Memory() int64 {
	data, err := os.ReadFile(cgroupV2MemFile)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	if s == "max" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func readCgroupV1Memory() int64 {
	data, err := os.ReadFile(cgroupV1MemFile)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	if v == cgroupV1Unlimited {
		return 0
	}
	return v
}
