// Package release downloads binaries from release archives verified against
// their published SHA-256 checksums.
package release

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Asset is a release archive and the checksum listing covering it.
type Asset struct {
	Name         string
	URL          string
	ChecksumsURL string
}

// Binary downloads the asset, verifies its checksum and returns the regular
// file in it whose path ends in binary, such as "bin/gh".
func Binary(ctx context.Context, client *http.Client, a Asset, binary string) ([]byte, error) {
	sums, err := Download(ctx, client, a.ChecksumsURL)
	if err != nil {
		return nil, err
	}
	want, err := Checksum(sums, a.Name)
	if err != nil {
		return nil, err
	}
	archive, err := Download(ctx, client, a.URL)
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s", a.Name)
	}
	data, err := Extract(archive, binary)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name, err)
	}
	return data, nil
}

// Download returns the body of url.
func Download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Checksum returns the SHA-256 of name from a sha256sum style listing.
func Checksum(sums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// Extract returns the regular file whose path ends in suffix from a gzipped
// tar archive.
func Extract(archive []byte, suffix string) ([]byte, error) {
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
