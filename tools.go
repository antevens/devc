package main

import (
	"archive/tar"
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
	"strings"
)

// cliTool is a command line tool installed from its official release
// archives. There is no maintained devcontainer feature for glab, and
// installing both the same way works in existing containers without a
// rebuild.
type cliTool struct {
	name string
	// latest returns the latest version and its download URLs by asset name.
	latest func() (string, map[string]string, error)
	// archive and checksums return asset names for a version.
	archive   func(version, arch string) string
	checksums func(version string) string
}

var cliTools = map[string]cliTool{
	github: {
		name: "gh",
		latest: func() (string, map[string]string, error) {
			var release struct {
				TagName string `json:"tag_name"`
				Assets  []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				} `json:"assets"`
			}
			err := apiCall(http.MethodGet, "https://api.github.com/repos/cli/cli/releases/latest", nil, nil, &release)
			assets := map[string]string{}
			for _, a := range release.Assets {
				assets[a.Name] = a.URL
			}
			return strings.TrimPrefix(release.TagName, "v"), assets, err
		},
		archive:   func(v, arch string) string { return fmt.Sprintf("gh_%s_linux_%s.tar.gz", v, arch) },
		checksums: func(v string) string { return fmt.Sprintf("gh_%s_checksums.txt", v) },
	},
	gitlab: {
		name: "glab",
		latest: func() (string, map[string]string, error) {
			var release struct {
				TagName string `json:"tag_name"`
				Assets  struct {
					Links []struct {
						Name string `json:"name"`
						URL  string `json:"url"`
					} `json:"links"`
				} `json:"assets"`
			}
			err := apiCall(http.MethodGet, "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/releases/permalink/latest", nil, nil, &release)
			assets := map[string]string{}
			for _, a := range release.Assets.Links {
				assets[a.Name] = a.URL
			}
			return strings.TrimPrefix(release.TagName, "v"), assets, err
		},
		archive:   func(v, arch string) string { return fmt.Sprintf("glab_%s_linux_%s.tar.gz", v, arch) },
		checksums: func(string) string { return "checksums.txt" },
	},
}

// installTool installs the tool in the container unless it is already on
// the remote user's PATH.
func (c *container) installTool(t cliTool, home string) error {
	if exec.Command("docker", "exec", "--user", c.user, c.id, "sh", "-lc", "command -v "+t.name).Run() == nil {
		return nil
	}
	arch, err := c.arch()
	if err != nil {
		return err
	}
	binary, err := downloadTool(t, arch, home)
	if err != nil {
		return err
	}
	a := newArchive(0, 0)
	a.addFile("usr/local/bin/"+t.name, binary, 0o755)
	data, err := a.close()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "devc: installed %s in the container\n", t.name)
	return c.extract("root", "/", data)
}

// arch returns the container's architecture as named in release archives.
func (c *container) arch() (string, error) {
	out, err := exec.Command("docker", "exec", c.id, "uname", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("uname in container: %w", err)
	}
	switch machine := strings.TrimSpace(string(out)); machine {
	case "x86_64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported container architecture %s", machine)
	}
}

// downloadTool returns the tool's binary for arch from the latest release,
// verified against the release checksums and cached in ~/.cache/devc.
func downloadTool(t cliTool, arch, home string) ([]byte, error) {
	version, assets, err := t.latest()
	if err != nil {
		return nil, fmt.Errorf("find latest %s release: %w", t.name, err)
	}
	cached := filepath.Join(cacheDir(home), t.name, version+"-"+arch, t.name)
	if binary, err := os.ReadFile(cached); err == nil {
		return binary, nil
	}

	name := t.archive(version, arch)
	archiveURL, sumsURL := assets[name], assets[t.checksums(version)]
	if archiveURL == "" || sumsURL == "" {
		return nil, fmt.Errorf("%s %s has no %s or checksums", t.name, version, name)
	}
	sums, err := download(sumsURL)
	if err != nil {
		return nil, err
	}
	want, err := checksum(sums, name)
	if err != nil {
		return nil, err
	}
	archive, err := download(archiveURL)
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s", name)
	}
	binary, err := extractBinary(archive, "bin/"+t.name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cached, binary, 0o755); err != nil {
		return nil, err
	}
	return binary, nil
}

// cacheDir returns the directory devc caches downloads in.
func cacheDir(home string) string {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "devc")
}

func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// checksum returns the SHA-256 of name from a sha256sum style listing.
func checksum(sums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// extractBinary returns the regular file whose path ends in suffix from a
// gzipped tar archive.
func extractBinary(archive []byte, suffix string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no %s in archive", suffix)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && (h.Name == suffix || strings.HasSuffix(h.Name, "/"+suffix)) {
			return io.ReadAll(tr)
		}
	}
}
