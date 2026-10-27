package forge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/release"
)

// tool is a forge's command line tool, installed from its latest release.
type tool struct {
	name string
	// latest returns the latest version and the URLs of its assets by name.
	latest func(ctx context.Context, fc *Client) (string, map[string]string, error)
	// archive and checksums return asset names for a version.
	archive   func(version, arch string) string
	checksums func(version string) string
}

var tools = map[Kind]tool{
	GitHub: {
		name: "gh",
		latest: func(ctx context.Context, fc *Client) (string, map[string]string, error) {
			var r struct {
				TagName string `json:"tag_name"`
				Assets  []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				} `json:"assets"`
			}
			err := fc.call(ctx, http.MethodGet, "https://api.github.com/repos/cli/cli/releases/latest", nil, nil, &r)
			assets := map[string]string{}
			for _, a := range r.Assets {
				assets[a.Name] = a.URL
			}
			return strings.TrimPrefix(r.TagName, "v"), assets, err
		},
		archive:   func(v, arch string) string { return fmt.Sprintf("gh_%s_linux_%s.tar.gz", v, arch) },
		checksums: func(v string) string { return fmt.Sprintf("gh_%s_checksums.txt", v) },
	},
	GitLab: {
		name: "glab",
		latest: func(ctx context.Context, fc *Client) (string, map[string]string, error) {
			var r struct {
				TagName string `json:"tag_name"`
				Assets  struct {
					Links []struct {
						Name string `json:"name"`
						URL  string `json:"url"`
					} `json:"links"`
				} `json:"assets"`
			}
			err := fc.call(ctx, http.MethodGet, "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/releases/permalink/latest", nil, nil, &r)
			assets := map[string]string{}
			for _, a := range r.Assets.Links {
				assets[a.Name] = a.URL
			}
			return strings.TrimPrefix(r.TagName, "v"), assets, err
		},
		archive:   func(v, arch string) string { return fmt.Sprintf("glab_%s_linux_%s.tar.gz", v, arch) },
		checksums: func(string) string { return "checksums.txt" },
	},
}

// InstallTools installs the command line tool of each forge kind in the
// container, unless it is already on the remote user's PATH. Binaries are
// cached in cacheDir. Installed tools are reported to out.
func (fc *Client) InstallTools(ctx context.Context, c *container.Container, forges []Forge, cacheDir string, out io.Writer) error {
	done := map[Kind]bool{}
	for _, f := range forges {
		if done[f.Kind] {
			continue
		}
		done[f.Kind] = true
		tl := tools[f.Kind]
		if c.HasCommand(tl.name) {
			continue
		}
		if err := fc.installTool(ctx, c, tl, cacheDir); err != nil {
			return fmt.Errorf("install %s: %w", tl.name, err)
		}
		fmt.Fprintf(out, "devc: installed %s in the container\n", tl.name)
	}
	return nil
}

func (fc *Client) installTool(ctx context.Context, c *container.Container, tl tool, cacheDir string) error {
	arch, err := c.Arch()
	if err != nil {
		return err
	}
	binary, err := fc.download(ctx, tl, arch, cacheDir)
	if err != nil {
		return err
	}
	a := archive.New(0, 0)
	a.AddFile("usr/local/bin/"+tl.name, binary, 0o755)
	data, err := a.Bytes()
	if err != nil {
		return err
	}
	return c.ExtractRoot(data)
}

// download returns the tool's binary for arch from its latest release, from
// cacheDir when it has been downloaded before.
func (fc *Client) download(ctx context.Context, tl tool, arch, cacheDir string) ([]byte, error) {
	version, assets, err := tl.latest(ctx, fc)
	if err != nil {
		return nil, fmt.Errorf("find latest release: %w", err)
	}
	cached := filepath.Join(cacheDir, tl.name, version+"-"+arch, tl.name)
	if binary, err := os.ReadFile(cached); err == nil {
		return binary, nil
	}

	name := tl.archive(version, arch)
	asset := release.Asset{Name: name, URL: assets[name], ChecksumsURL: assets[tl.checksums(version)]}
	if asset.URL == "" || asset.ChecksumsURL == "" {
		return nil, fmt.Errorf("release %s has no %s or checksums", version, name)
	}
	binary, err := release.Binary(ctx, fc.HTTP, asset, "bin/"+tl.name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cached, binary, 0o755); err != nil {
		return nil, err
	}
	return binary, nil
}
