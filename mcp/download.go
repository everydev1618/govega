package mcp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ghRelease is the subset of the GitHub Releases API response we need.
type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

// ghAsset is a single release asset.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// EnsureBinary resolves a command to a binary path, downloading from GitHub
// Releases if necessary.
//
// Resolution order:
//  1. System PATH (exec.LookPath)
//  2. Cached binary in ~/.vega/bin/
//  3. Download latest release from GitHub
func EnsureBinary(ctx context.Context, githubRepo, command string) (string, error) {
	// 1. Check system PATH.
	if p, err := exec.LookPath(command); err == nil {
		return p, nil
	}

	// 2. Check cached binary.
	binDir := vegaBinPath()
	cached := filepath.Join(binDir, command)
	if _, err := os.Stat(cached); err == nil {
		return cached, nil
	}

	// 3. Download from GitHub.
	if githubRepo == "" {
		return "", fmt.Errorf("command %q not found and no GitHubRepo configured", command)
	}

	slog.Info("downloading MCP server binary", "command", command, "repo", githubRepo)

	asset, err := findAsset(ctx, githubRepo)
	if err != nil {
		return "", fmt.Errorf("find release asset: %w", err)
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}

	if err := downloadAndExtract(ctx, asset, command, cached); err != nil {
		return "", fmt.Errorf("download binary: %w", err)
	}

	slog.Info("downloaded MCP server binary", "path", cached)
	return cached, nil
}

// findAsset queries the GitHub Releases API for the latest release and returns
// the asset matching the current OS/architecture.
func findAsset(ctx context.Context, repo string) (ghAsset, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ghAsset{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ghAsset{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ghAsset{}, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ghAsset{}, fmt.Errorf("decode release: %w", err)
	}

	// GoReleaser names assets like: project_version_os_arch.tar.gz
	// Match by looking for _{os}_{arch} in the asset name.
	suffix := fmt.Sprintf("_%s_%s", runtime.GOOS, runtime.GOARCH)

	for _, a := range release.Assets {
		if strings.Contains(a.Name, suffix) {
			return a, nil
		}
	}

	return ghAsset{}, fmt.Errorf("no asset found for %s/%s in %s", runtime.GOOS, runtime.GOARCH, repo)
}

// downloadAndExtract downloads an asset archive and extracts the named binary.
func downloadAndExtract(ctx context.Context, asset ghAsset, command, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	// Write to temp file so we can seek (needed for zip).
	tmp, err := os.CreateTemp("", "vega-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("save download: %w", err)
	}

	if strings.HasSuffix(asset.Name, ".zip") {
		return extractZip(tmp.Name(), command, dest)
	}
	return extractTarGz(tmp.Name(), command, dest)
}

// extractTarGz extracts a binary from a .tar.gz archive.
func extractTarGz(archive, command, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Match by base name — GoReleaser puts the binary at the root of the archive.
		if filepath.Base(hdr.Name) == command && hdr.Typeflag == tar.TypeReg {
			return writeFile(tr, dest)
		}
	}

	return fmt.Errorf("binary %q not found in archive", command)
}

// extractZip extracts a binary from a .zip archive.
func extractZip(archive, command, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) == command {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return writeFile(rc, dest)
		}
	}

	return fmt.Errorf("binary %q not found in archive", command)
}

// vegaBinPath returns the directory for auto-downloaded binaries (~/.vega/bin).
// This duplicates vega.BinPath() to avoid an import cycle.
func vegaBinPath() string {
	home := os.Getenv("VEGA_HOME")
	if home == "" {
		u, _ := os.UserHomeDir()
		home = filepath.Join(u, ".vega")
	}
	return filepath.Join(home, "bin")
}

// writeFile writes the binary from r to dest with executable permissions.
func writeFile(r io.Reader, dest string) error {
	// Write to a temp file in the same directory, then rename for atomicity.
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}
