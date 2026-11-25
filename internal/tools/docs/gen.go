package docs

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
func (s *Site) Files() ([]File, error) {
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
	prefix := s.Opts.Assets
	out = append(out, File{Path: prefix + "data.js", Content: s.dataJS()})
	for _, a := range s.Assets() {
		out = append(out, File{Path: prefix + a.Path, Content: a.Content})
	}
	return out, nil
}

// DataJS returns the search index and hover cards as the script pages load.
func (s *Site) DataJS() []byte { return s.dataJS() }

// Register installs the docs renderers and the `docs` generation target.
func Register(r *registry.Registry) {
	registerRenderers(r)
	if err := r.SetGenerator(registry.Generator{Path: "docs.site", Target: "docs", Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
		gi := in.(*gen.Input)
		versions := releasedVersions(filepath.Join(gi.Dir, OutDir))
		s := New(gi.Tree, gi.Checker, Options{Registry: r, Versions: versions})
		files, err := s.Files()
		if err != nil {
			return nil, err
		}
		var out []registry.GeneratedFile
		for _, f := range files {
			out = append(out, registry.GeneratedFile{Path: OutDir + "/" + f.Path, Content: f.Content, Ignored: true})
		}
		vj, _ := json.Marshal(versions)
		out = append(out, registry.GeneratedFile{Path: OutDir + "/versions.json", Content: append(vj, '\n'), Ignored: true})
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
