package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtract(t *testing.T) {
	archive := tarball(t, map[string]string{"gh_1_linux_amd64/LICENSE": "l", "gh_1_linux_amd64/bin/gh": "binary"})
	if got, err := Extract(archive, "bin/gh"); err != nil || string(got) != "binary" {
		t.Errorf("Extract = %q, %v", got, err)
	}
	if _, err := Extract(archive, "bin/glab"); err == nil {
		t.Error("missing binary: want an error")
	}
}

func TestChecksum(t *testing.T) {
	sums := []byte("aaa  other.tar.gz\nbbb *gh_1_linux_amd64.tar.gz\n")
	if sum, err := Checksum(sums, "gh_1_linux_amd64.tar.gz"); err != nil || sum != "bbb" {
		t.Errorf("Checksum = %q, %v", sum, err)
	}
	if _, err := Checksum(sums, "missing"); err == nil {
		t.Error("missing checksum: want an error")
	}
}

func TestBinary(t *testing.T) {
	archive := tarball(t, map[string]string{"bin/glab": "glab"})
	sum := sha256.Sum256(archive)
	good := fmt.Sprintf("%s  glab.tar.gz\n", hex.EncodeToString(sum[:]))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/glab.tar.gz":
			w.Write(archive)
		case "/good":
			w.Write([]byte(good))
		case "/bad":
			w.Write([]byte("0000  glab.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	asset := Asset{Name: "glab.tar.gz", URL: srv.URL + "/glab.tar.gz", ChecksumsURL: srv.URL + "/good"}
	if got, err := Binary(context.Background(), srv.Client(), asset, "bin/glab"); err != nil || string(got) != "glab" {
		t.Errorf("Binary = %q, %v", got, err)
	}
	asset.ChecksumsURL = srv.URL + "/bad"
	if _, err := Binary(context.Background(), srv.Client(), asset, "bin/glab"); err == nil {
		t.Error("checksum mismatch: want an error")
	}
}
