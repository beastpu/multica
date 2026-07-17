package cli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/selfexec"
)

const DefaultUpdateDownloadTimeout = 120 * time.Second

// DefaultDownloadBaseURL is Lilith's release download host — the same origin
// the Desktop app and the /download page pull installers from (see
// server/internal/handler/downloads.go and apps/desktop/electron-builder.yml).
// Self-update fetches the CLI tarball, its checksum manifest and the version
// pointer from here. Overridable via MULTICA_DOWNLOAD_BASE (self-host / tests).
const DefaultDownloadBaseURL = "https://multica.lilithgames.com/api/downloads"

// downloadBaseURL resolves the release host, honoring the MULTICA_DOWNLOAD_BASE
// override. Trailing slashes are trimmed so callers can append "/<file>".
func downloadBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("MULTICA_DOWNLOAD_BASE")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultDownloadBaseURL
}

// GitHubRelease carries the latest CLI release tag. The name is retained for
// compatibility with the daemon auto-update loop and its tests; the source is
// now Lilith's OSS download host (latest-cli.txt), not the GitHub API.
type GitHubRelease struct {
	TagName string
}

// IsReleaseVersion reports whether v looks like a tagged release version
// (e.g. "0.1.13", "v0.1.13") rather than a dev build (e.g. an empty version
// or a `git describe`–style "v0.2.15-235-gdaf0e935"). The auto-update poller
// uses this to skip self-update for source builds, where downgrading to a
// public release would clobber unreleased changes.
func IsReleaseVersion(v string) bool {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// IsNewerVersion reports whether latest is strictly newer than current. Both
// arguments may carry an optional "v" prefix; non-numeric tails are ignored
// (a 4th component, pre-release tag, etc.). Returns false if either side
// cannot be parsed — the caller treats that as "stay on current".
func IsNewerVersion(latest, current string) bool {
	l, ok := parseReleaseVersion(latest)
	if !ok {
		return false
	}
	c, ok := parseReleaseVersion(current)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseReleaseVersion extracts the three numeric components of v. Returns
// (parts, true) on success; (_, false) when v is missing, malformed, or
// carries any non-numeric tail (a dev-describe suffix, a 4th component, a
// pre-release tag, etc.). The strict shape is intentional: this is the only
// parser used by IsNewerVersion, and the autoUpdateLoop must never silently
// downgrade a developer build to a public release just because the
// dev-describe patch happened to look numeric after trimming.
func parseReleaseVersion(v string) ([3]int, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		if p == "" {
			return [3]int{}, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return [3]int{}, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func releaseArchiveExtension(goos string) string {
	if goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

func normalizeReleaseTag(targetVersion string) string {
	tag := strings.TrimSpace(targetVersion)
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return tag
}

// releaseArchiveName is the OSS object name for the CLI tarball of a given
// version + platform, matching what the `cli` job in
// .github/workflows/lilith-desktop-release.yml publishes:
//
//	multica-cli-<version>-<goos>-<goarch>.<tar.gz|zip>
func releaseArchiveName(version, goos, goarch string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return fmt.Sprintf("multica-cli-%s-%s-%s.%s", v, goos, goarch, releaseArchiveExtension(goos))
}

// checksumManifestName is the OSS object name for a release's checksum
// manifest. It is versioned (and therefore immutable), so a fetched manifest
// always corresponds to the tarball named by releaseArchiveName for the same
// version — no chance of pairing a tarball with a stale, overwritten manifest.
func checksumManifestName(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return fmt.Sprintf("multica-cli-%s-checksums.txt", v)
}

// parseChecksumManifest reads a GoReleaser-style "<sha256>  <filename>"
// manifest and returns the lowercase hex SHA-256 for assetName. Returns an
// error if the asset is absent so a typo (or the wrong manifest from a
// different release) fails closed rather than silently disabling
// verification.
func parseChecksumManifest(manifest []byte, assetName string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(manifest))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		// GoReleaser's default separator is two spaces; some tools use one
		// or pad with tabs. strings.Fields handles all of those at once.
		if len(fields) < 2 {
			continue
		}
		if fields[1] == assetName {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read checksum manifest: %w", err)
	}
	return "", fmt.Errorf("checksum for %q not found in manifest", assetName)
}

// verifyAssetSHA256 returns nil when the SHA-256 of data matches the lowercase
// hex expected value, or an error otherwise. The error includes both digests
// so a corrupted asset is diagnosable from the log without re-downloading.
func verifyAssetSHA256(data []byte, expectedHex, assetName string) error {
	if expectedHex == "" {
		return fmt.Errorf("empty expected checksum for %q", assetName)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expectedHex) {
		return fmt.Errorf("checksum mismatch for %q: expected %s, got %s", assetName, expectedHex, actual)
	}
	return nil
}

// FetchLatestRelease reads the current CLI version from Lilith's OSS version
// pointer (latest-cli.txt) — the mutable file the `cli` release job overwrites
// on every publish. The returned tag feeds IsNewerVersion / UpdateViaDownload.
func FetchLatestRelease() (*GitHubRelease, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := downloadBaseURL() + "/latest-cli.txt"
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version pointer returned %d from %s", resp.StatusCode, url)
	}

	// The pointer is a short one-line version string; cap the read so a
	// misconfigured object can't stream unbounded data into the daemon.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(string(body))
	if i := strings.IndexAny(version, "\r\n"); i >= 0 {
		version = strings.TrimSpace(version[:i])
	}
	if version == "" {
		return nil, fmt.Errorf("empty version pointer at %s", url)
	}
	return &GitHubRelease{TagName: version}, nil
}

// knownBrewPrefixes lists the install roots Homebrew uses on each platform.
// Order is irrelevant — the prefixes do not nest.
var knownBrewPrefixes = []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"}

// MatchKnownBrewPrefix returns the Homebrew prefix whose Cellar contains path,
// or "" if path is not under a known Cellar. It is the offline equivalent of
// `brew --prefix`: callers reach for it when `brew --prefix` is unavailable
// (brew not on PATH) but the binary's path still betrays its install root.
func MatchKnownBrewPrefix(path string) string {
	for _, prefix := range knownBrewPrefixes {
		if strings.HasPrefix(path, prefix+"/Cellar/") {
			return prefix
		}
	}
	return ""
}

// IsBrewInstall checks whether the running multica binary was installed via Homebrew.
func IsBrewInstall() bool {
	exePath, err := selfexec.Resolve()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		resolved = exePath
	}

	brewPrefix := GetBrewPrefix()
	if brewPrefix != "" && strings.HasPrefix(resolved, brewPrefix) {
		return true
	}

	return MatchKnownBrewPrefix(resolved) != ""
}

// GetBrewPrefix returns the Homebrew prefix by running `brew --prefix`, or empty string.
func GetBrewPrefix() string {
	out, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// UpdateViaBrew runs `brew upgrade multica-ai/tap/multica`.
// Returns the combined output and any error.
func UpdateViaBrew() (string, error) {
	cmd := exec.Command("brew", "upgrade", "multica-ai/tap/multica")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("brew upgrade failed: %w", err)
	}
	return string(out), nil
}

func updateDownloadTimeoutOrDefault(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultUpdateDownloadTimeout
	}
	return timeout
}

// fetchURLBytes does a GET with the given timeout and returns the response
// body in full. Used for the checksum manifest (tiny) and the release
// archive (single-digit MB). The checksum verification path requires buffered
// bytes so streaming would just push the buffer into the caller anyway.
func fetchURLBytes(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: updateDownloadTimeoutOrDefault(timeout)}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// UpdateViaDownload downloads the latest release binary from GitHub and replaces
// the current executable in-place. Returns the combined output message and any error.
func UpdateViaDownload(targetVersion string) (string, error) {
	return UpdateViaDownloadWithTimeout(targetVersion, DefaultUpdateDownloadTimeout)
}

// UpdateViaDownloadWithTimeout downloads the latest release binary with a caller-selected timeout.
func UpdateViaDownloadWithTimeout(targetVersion string, downloadTimeout time.Duration) (string, error) {
	// Determine current binary path.
	exePath, err := selfexec.Resolve()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve symlink: %w", err)
	}

	// Lilith publishes the standalone CLI for Linux only; on macOS / Windows
	// the CLI ships inside the Desktop app, which owns its own updates. There
	// is no OSS artifact to fetch on those platforms, so fail with a pointer
	// to the download page rather than a bare 404.
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("no standalone CLI build for %s/%s; the CLI ships inside the Desktop app — see %s/download",
			runtime.GOOS, runtime.GOARCH, "https://multica.lilithgames.com")
	}

	base := downloadBaseURL()
	version := strings.TrimPrefix(normalizeReleaseTag(targetVersion), "v")
	assetName := releaseArchiveName(version, runtime.GOOS, runtime.GOARCH)
	downloadURL := base + "/" + assetName

	// Pull the checksum manifest first so a half-published release (tarball
	// uploaded but checksums not yet) fails before we eat the archive's
	// bandwidth. The manifest is versioned + immutable on OSS, so it always
	// corresponds to the tarball named above.
	timeout := updateDownloadTimeoutOrDefault(downloadTimeout)
	manifestData, err := fetchURLBytes(base+"/"+checksumManifestName(version), timeout)
	if err != nil {
		return "", fmt.Errorf("download checksum manifest: %w", err)
	}
	expectedSum, err := parseChecksumManifest(manifestData, assetName)
	if err != nil {
		return "", fmt.Errorf("parse checksum manifest: %w", err)
	}

	// Buffer the archive into memory so we can verify the full SHA-256
	// before writing anything to disk. Release archives are ~10–30 MB; the
	// extraction code already buffers zip archives in full (random access
	// requirement), so this is not a new memory cost on Windows. For tar.gz
	// it adds a single in-RAM copy, which is preferable to running the
	// untrusted bytes through gzip+tar extraction before the SHA-256 check.
	archiveData, err := fetchURLBytes(downloadURL, timeout)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	if err := verifyAssetSHA256(archiveData, expectedSum, assetName); err != nil {
		// Do NOT extract or replace; the next poll tick will retry. A
		// corrupted asset is rare enough that retrying through the same
		// CDN is the right default; persistent failures will surface in
		// the daemon log.
		return "", fmt.Errorf("verify download: %w", err)
	}

	// Extract the binary from the archive.
	binaryName := "multica"
	if runtime.GOOS == "windows" {
		binaryName = "multica.exe"
	}
	var binaryData []byte
	if runtime.GOOS == "windows" {
		binaryData, err = extractBinaryFromZip(bytes.NewReader(archiveData), binaryName)
	} else {
		binaryData, err = extractBinaryFromTarGz(bytes.NewReader(archiveData), binaryName)
	}
	if err != nil {
		return "", fmt.Errorf("extract binary: %w", err)
	}

	// Atomic replace: write to temp file, then rename over the original.
	dir := filepath.Dir(exePath)
	tmpFile, err := os.CreateTemp(dir, "multica-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(binaryData); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Preserve original file permissions.
	info, err := os.Stat(exePath)
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("stat original binary: %w", err)
	}
	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp file: %w", err)
	}

	// Replace the original binary. On Windows this moves the running executable
	// aside first; on Unix a plain rename over the running inode is fine.
	if err := replaceBinary(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("replace binary: %w", err)
	}

	return fmt.Sprintf("Downloaded %s and replaced %s", assetName, exePath), nil
}

// extractBinaryFromTarGz reads a .tar.gz stream and returns the contents of the
// named file entry.
func extractBinaryFromTarGz(r io.Reader, name string) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("binary %q not found in archive", name)
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		// Match the binary name (may be prefixed with a directory).
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read binary: %w", err)
			}
			return data, nil
		}
	}
}

// extractBinaryFromZip reads a .zip stream and returns the contents of the
// named file entry. The zip format requires random access, so the full archive
// is buffered in memory.
func extractBinaryFromZip(r io.Reader, name string) ([]byte, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read zip data: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}

	for _, f := range zr.File {
		if filepath.Base(f.Name) == name && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry: %w", err)
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, fmt.Errorf("read binary: %w", err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", name)
}
