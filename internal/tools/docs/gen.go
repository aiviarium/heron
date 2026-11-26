package docs

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
)

// The built theme and islands, from web/docs (`pnpm --filter @heron/docs
// build` copies them here), so generating docs needs no Node.
//
//go:embed assets
var assets embed.FS

// OutDir is where `heron generate docs` writes the site, relative to the
// project directory. Released versions go under OutDir/v/<version>/.
const OutDir = ".heron/docs"

// Asset is a file the pages load.
type Asset struct {
	Path    string // relative to the assets prefix, such as "docs.css"
	Content []byte
	Type    string
}

// Assets returns the theme and the islands' scripts.
func Assets() []Asset {
	var out []Asset
	fs.WalkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		rel := strings.TrimPrefix(p, "assets/")
		out = append(out, Asset{Path: rel, Content: b, Type: mimeOf(rel)})
		return nil
	})
	return out
}

// Assets returns the built-in assets and the theme's files, which a
// server serves under the assets prefix.
func (s *Site) Assets() []Asset {
	out := Assets()
	if s.Theme != nil {
		out = append(out, s.Theme.Files...)
	}
	return out
}

func mimeOf(p string) string {
	switch filepath.Ext(p) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".woff2":
		return "font/woff2"
	case ".svg":
		return "image/svg+xml"
	}
	return "application/octet-stream"
}

// File is a file of the generated site.
type File struct {
	Path    string
	Content []byte
}

// Files renders the whole site: every page, the data script and the
// assets, as plain files.
func (s *Site) Files() ([]File, error) { return s.files(true) }

// files renders the site's pages and data script, and the assets when
// withAssets is set.
func (s *Site) files(withAssets bool) ([]File, error) {
	if s.ThemeErr != nil {
		return nil, s.ThemeErr
	}
	if s.RenderersErr != nil {
		return nil, s.RenderersErr
	}
	var out []File
	for _, p := range s.Pages {
		h, err := s.RenderPage(p)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: p.File, Content: []byte(h)})
	}
	out = append(out, File{Path: s.Opts.Data, Content: s.dataJS()})
	if withAssets {
		for _, a := range s.Assets() {
			out = append(out, File{Path: "_assets/" + a.Path, Content: a.Content})
		}
	}
	return out, nil
}

// DataJS returns the search index and hover cards as the script pages load.
func (s *Site) DataJS() []byte { return s.dataJS() }

// Register installs the docs renderers and the `docs` generation target.
func Register(r *registry.Registry) {
	registerRenderers(r)
	registerAsyncAPI(r)
	if err := r.SetGenerator(registry.Generator{Path: "docs.site", Target: "docs", Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
		gi := in.(*gen.Input)
		versions := releasedVersions(filepath.Join(gi.Dir, OutDir))
		files, err := NewLenses(gi.Tree, gi.Checker, Options{Registry: r, Versions: versions}).Files()
		if err != nil {
			return nil, err
		}
		var out []registry.GeneratedFile
		for _, f := range files {
			out = append(out, registry.GeneratedFile{Path: OutDir + "/" + f.Path, Content: f.Content, Ignored: true})
		}
		vj, _ := json.Marshal(versions)
		out = append(out, registry.GeneratedFile{Path: OutDir + "/versions.json", Content: append(vj, '\n'), Ignored: true})
		manifest, err := removeStale(filepath.Join(gi.Dir, OutDir), out)
		if err != nil {
			return nil, err
		}
		out = append(out, registry.GeneratedFile{Path: OutDir + "/" + manifestName, Content: manifest, Ignored: true})
		return out, nil
	}}); err != nil {
		panic(err)
	}
}

// asyncAPIDir is where `heron generate asyncapi` writes its documents.
const asyncAPIDir = ".heron/asyncapi"

func registerAsyncAPI(r *registry.Registry) {
	if err := r.SetGenerator(registry.Generator{Path: "docs.asyncapi", Target: "asyncapi", OnRequest: true, Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
		gi := in.(*gen.Input)
		var out []registry.GeneratedFile
		docs := AsyncAPI(gi.Tree, gi.Checker)
		names := make([]string, 0, len(docs))
		for n := range docs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, registry.GeneratedFile{Path: asyncAPIDir + "/" + n, Content: docs[n], Ignored: true})
		}
		return out, nil
	}}); err != nil {
		panic(err)
	}
}

// releasedVersions lists the builds already written under OutDir/v/.
func releasedVersions(dir string) []string {
	ents, err := os.ReadDir(filepath.Join(dir, "v"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// manifestName is the file listing what the last run wrote in OutDir.
const manifestName = ".manifest"

// removeStale removes the files the previous run's manifest lists that
// this run doesn't write, and returns this run's manifest. A file the
// previous manifest doesn't list is someone else's and stays.
func removeStale(dir string, files []registry.GeneratedFile) ([]byte, error) {
	now := map[string]bool{}
	var lines []string
	for _, f := range files {
		rel := strings.TrimPrefix(f.Path, OutDir+"/")
		now[rel] = true
		lines = append(lines, rel)
	}
	sort.Strings(lines)
	if old, err := os.ReadFile(filepath.Join(dir, manifestName)); err == nil {
		for _, rel := range strings.Split(string(old), "\n") {
			rel = strings.TrimSpace(rel)
			if rel == "" || now[rel] || strings.Contains(rel, "..") {
				continue
			}
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// assetVersions are the content hashes of the built-in assets, which the
// pages put in the assets' URLs so a cache keeps an asset until it
// changes.
var assetVersions = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	for _, a := range Assets() {
		out[a.Path] = contentVersion(a.Content)
	}
	return out
})

func assetVersion(path string) string { return assetVersions()[path] }

// contentVersion is a short hash of a file's content.
func contentVersion(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}
