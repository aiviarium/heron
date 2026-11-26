package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
	"github.com/aiviarium/heron/std"
)

// The API's errors, by the id each declares.
func errNotInWorkspace(p string) error {
	return route.Coded(http.StatusBadRequest, "server.NOT_IN_WORKSPACE", "%s is not a path in the workspace", p)
}

func errNoSuchFile(p string) error {
	return route.Coded(http.StatusNotFound, "server.NO_SUCH_FILE", "%s: no such file", p)
}

func errFileExists(p string) error {
	return route.Coded(http.StatusConflict, "server.FILE_EXISTS", "%s already exists", p)
}

func errFileChanged(p string) error {
	return route.Coded(http.StatusPreconditionFailed, "server.FILE_CHANGED", "%s changed since it was read", p)
}

// abs is Workspace.Abs with the API's error.
func (s *service) abs(rel string) (string, error) {
	id, err := s.ws.Abs(rel)
	if err != nil {
		return "", errNotInWorkspace(rel)
	}
	return string(id), nil
}

// skipped are the folders a listing leaves out.
var skipped = map[string]bool{".git": true, ".heron": true}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// etag is the entity tag of a file's content.
func etag(b []byte) string {
	h := sha256.Sum256(b)
	return `"` + hex.EncodeToString(h[:12]) + `"`
}

func (s *service) ListFiles(ctx context.Context, dir string) (*api.FileList, error) {
	root := s.ws.Root
	if dir != "" && dir != "." {
		d, err := s.abs(dir)
		if err != nil {
			return nil, err
		}
		root = d
	}
	inTree := map[string]bool{}
	_ = s.ws.Analyze(nil, func(a *Analysis) error {
		for _, f := range a.Files() {
			inTree[f] = true
		}
		return nil
	})
	out := &api.FileList{Entries: []api.FileEntry{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return errNoSuchFile(dir)
			}
			return nil
		}
		if p == root {
			return nil
		}
		if d.IsDir() && skipped[d.Name()] {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel := s.ws.Rel(resolveID(p))
		e := api.FileEntry{Path: rel, Dir: d.IsDir(), Mtime: rfc3339(info.ModTime()), InTree: inTree[rel]}
		if !d.IsDir() {
			e.Size = info.Size()
		}
		out.Entries = append(out.Entries, e)
		return nil
	})
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out, err
}

func (s *service) ReadFile(ctx context.Context, rel, encoding string) (*api.FileContent, error) {
	p, err := s.abs(rel)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errNoSuchFile(rel)
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, errNoSuchFile(rel)
	}
	return content(rel, b, info.ModTime(), encoding), nil
}

// content is a file's content as the API answers it: text when it's UTF-8
// and base64 wasn't asked for, base64 otherwise.
func content(rel string, b []byte, mtime time.Time, encoding string) *api.FileContent {
	out := &api.FileContent{Path: rel, Size: int64(len(b)), Mtime: rfc3339(mtime), Etag: etag(b), Encoding: "utf-8"}
	if encoding == "base64" || !utf8.Valid(b) {
		out.Encoding = "base64"
		out.Text = base64.StdEncoding.EncodeToString(b)
	} else {
		out.Text = string(b)
	}
	return out
}

func (s *service) saved(rel, p string) (*api.Saved, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	out := &api.Saved{Path: rel, Mtime: rfc3339(info.ModTime())}
	if !info.IsDir() {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out.Size, out.Etag = int64(len(b)), etag(b)
	}
	return out, nil
}

func (s *service) WriteFile(ctx context.Context, req *api.WriteRequest) (*api.Saved, error) {
	p, err := s.abs(req.Path)
	if err != nil {
		return nil, err
	}
	data := []byte(req.Text)
	if req.Encoding != nil && *req.Encoding == "base64" {
		if data, err = base64.StdEncoding.DecodeString(req.Text); err != nil {
			return nil, route.Errorf(http.StatusBadRequest, "invalid body: the text isn't base64: %v", err)
		}
	}
	s.ws.writeMu.Lock()
	defer s.ws.writeMu.Unlock()
	if req.IfMatch != nil {
		old, err := os.ReadFile(p)
		switch {
		case err == nil && etag(old) != *req.IfMatch:
			return nil, errFileChanged(req.Path)
		case err != nil && *req.IfMatch != "":
			return nil, errFileChanged(req.Path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return nil, err
	}
	s.ws.Invalidate()
	s.ws.events.poke()
	return s.saved(req.Path, p)
}

func (s *service) DeleteFile(ctx context.Context, rel string) (*api.Removed, error) {
	p, err := s.abs(rel)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err != nil {
		return nil, errNoSuchFile(rel)
	}
	if err := os.Remove(p); err != nil {
		return nil, route.Errorf(http.StatusConflict, "%s: %v", rel, unwrapPath(err))
	}
	s.ws.Invalidate()
	s.ws.events.poke()
	return &api.Removed{Path: rel}, nil
}

func (s *service) RenameFile(ctx context.Context, req *api.RenameRequest) (*api.Saved, error) {
	from, err := s.abs(req.From)
	if err != nil {
		return nil, err
	}
	to, err := s.abs(req.To)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(from); err != nil {
		return nil, errNoSuchFile(req.From)
	}
	if _, err := os.Stat(to); err == nil {
		return nil, errFileExists(req.To)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(from, to); err != nil {
		return nil, err
	}
	s.ws.Invalidate()
	s.ws.events.poke()
	return s.saved(req.To, to)
}

func (s *service) MakeDir(ctx context.Context, req *api.PathRequest) (*api.Saved, error) {
	p, err := s.abs(req.Path)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return nil, errFileExists(req.Path)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, err
	}
	s.ws.events.poke()
	return s.saved(req.Path, p)
}

func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func (s *service) StdFiles(ctx context.Context) (*api.StdFiles, error) {
	out := &api.StdFiles{Files: []string{}}
	_ = fs.WalkDir(std.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out.Files = append(out.Files, "std/"+p)
		}
		return nil
	})
	sort.Strings(out.Files)
	return out, nil
}

func (s *service) StdSource(ctx context.Context, p string) (*api.FileContent, error) {
	rel := strings.TrimPrefix(path.Clean("/"+p), "/")
	rel = strings.TrimPrefix(rel, "std/")
	b, err := fs.ReadFile(std.FS, rel)
	if err != nil || !strings.HasPrefix(p, "std/") {
		return nil, errNoSuchFile(p)
	}
	return content(p, b, time.Time{}, ""), nil
}
