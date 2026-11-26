package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/records"
	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
)

// service implements the base API's ops.
type service struct {
	shell *Shell
	ws    *Workspace
	exts  []Extension
}

var _ api.Service = (*service)(nil)

func (s *service) Workspace(ctx context.Context) (*api.Workspace, error) {
	out := &api.Workspace{Name: filepath.Base(s.ws.Root), Root: s.ws.Root, RootUri: rootURI(s.ws.Root), Entry: s.ws.Rel(s.ws.Entry), Branch: s.ws.Branch(), Files: []string{}}
	err := s.ws.Analyze(nil, func(a *Analysis) error {
		out.Files = append(out.Files, a.Files()...)
		return nil
	})
	return out, err
}

func (s *service) Problems(ctx context.Context, req *api.TreeRequest) (*api.Problems, error) {
	out := &api.Problems{Problems: []api.Problem{}}
	err := s.ws.Analyze(req.Overlays, func(a *Analysis) error {
		add := func(ds diag.List, source string) {
			for _, d := range ds {
				out.Problems = append(out.Problems, a.problem(d, source))
			}
		}
		add(a.Res.Diags, "check")
		add(a.Lint(), "lint")
		return nil
	})
	return out, err
}

func (a *Analysis) problem(d diag.Diagnostic, source string) api.Problem {
	l, c := a.Pos(d.File, d.Start)
	el, ec := a.Pos(d.File, d.End)
	sev := "error"
	switch d.Severity {
	case diag.Warning:
		sev = "warning"
	case diag.Error:
	default:
		sev = "info"
	}
	file := d.File
	if filepath.IsAbs(file) {
		file = a.W.Rel(resolveID(file))
	}
	return api.Problem{File: file, Line: int64(l), Column: int64(c), EndLine: int64(el), EndColumn: int64(ec),
		Severity: sev, Code: d.Code, Message: d.Message, Source: source}
}

func (s *service) Outline(ctx context.Context, req *api.OutlineRequest) (*api.Outline, error) {
	out := &api.Outline{File: req.File, Nodes: []api.OutlineNode{}}
	id, err := s.ws.Abs(req.File)
	if err != nil {
		return nil, errNotInWorkspace(req.File)
	}
	err = s.ws.Analyze(req.Overlays, func(a *Analysis) error {
		out.Nodes = a.outline(id)
		return nil
	})
	return out, err
}

func (s *service) Preview(ctx context.Context, req *api.PreviewRequest) (*api.Preview, error) {
	id, err := s.ws.Abs(req.File)
	if err != nil {
		return nil, errNotInWorkspace(req.File)
	}
	var out *api.Preview
	err = s.ws.Analyze(req.Overlays, func(a *Analysis) error {
		off := 0
		if req.Offset != nil {
			off = int(*req.Offset)
		}
		if req.Position != nil {
			text, ok := req.Overlays[req.File]
			if !ok {
				b, _ := os.ReadFile(string(id))
				text = string(b)
			}
			off = utf16Offset(text, int(req.Position.Line), int(req.Position.Character))
		}
		out = a.preview(s.shell.Renderer, id, off)
		return nil
	})
	if err != nil {
		return nil, err
	}
	res, _ := s.Results(ctx, out.Path)
	out.Results = *res
	return out, nil
}

func (s *service) Results(ctx context.Context, path string) (*api.NodeResults, error) {
	out := &api.NodeResults{Path: path, Results: []api.NodeResult{}}
	out.Results = append(out.Results, runResults(s.ws, path)...)
	for _, e := range s.exts {
		for _, p := range e.Results {
			out.Results = append(out.Results, p(ctx, s.ws, path)...)
		}
	}
	return out, nil
}

func (s *service) store() *records.Store { return records.Open(s.ws.Root, records.Config{}, s.ws.Reg) }

func (s *service) GetRecord(ctx context.Context, id string) (*api.Record, error) {
	st := s.store()
	for _, k := range s.ws.Reg.RecordKinds() {
		if r, err := st.Read(k.Name, id); err == nil {
			return &api.Record{Kind: r.Kind, ID: r.ID, Time: r.Time.UTC().Format("2006-01-02T15:04:05.000Z"), Data: r.Data}, nil
		}
	}
	return nil, route.Coded(http.StatusNotFound, "server.NO_RECORD", "no record %s", id)
}

// runResults is the built-in result provider: each task's last run.
func runResults(w *Workspace, prefix string) []api.NodeResult {
	rs, err := records.Open(w.Root, records.Config{}, w.Reg).List(records.RunKind)
	if err != nil {
		return nil
	}
	latest := map[string]*records.Record{}
	for _, r := range rs {
		task, _ := r.Data["task"].(string)
		if task == "" || !under(task, prefix) {
			continue
		}
		if l := latest[task]; l == nil || r.Time.After(l.Time) {
			latest[task] = r
		}
	}
	var out []api.NodeResult
	for task, r := range latest {
		out = append(out, runResult(task, r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func runResult(task string, r *records.Record) api.NodeResult {
	status, _ := r.Data["status"].(string)
	summary := ""
	switch ms := r.Data["duration_ms"].(type) {
	case float64:
		summary = duration(int64(ms))
	case int64:
		summary = duration(ms)
	}
	id, t := r.ID, r.Time.UTC().Format("2006-01-02T15:04:05Z")
	return api.NodeResult{Source: "run", Path: task, Status: status, Summary: summary, RecordID: &id, Time: &t}
}

func duration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}

func under(path, prefix string) bool {
	return prefix == "" || path == prefix || strings.HasPrefix(path, prefix+".")
}

// runs serializes task runs per task, so the gutter can't start one twice.
var runs sync.Map

func (s *service) RunTask(ctx context.Context, req *api.RunRequest, send func(*api.RunEvent) error) error {
	if _, busy := runs.LoadOrStore(req.Path, true); busy {
		return route.Coded(http.StatusConflict, "server.ALREADY_RUNNING", "%s is already running", req.Path)
	}
	defer runs.Delete(req.Path)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := send(&api.RunEvent{Type: "start", Result: &api.NodeResult{Source: "run", Path: req.Path, Status: "running"}}); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, "run", "--yes", "--entry", string(s.ws.Entry), req.Path)
	cmd.Dir = s.ws.Root
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); pw.Close() }()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		text := sc.Text()
		if err := send(&api.RunEvent{Type: "output", Text: &text}); err != nil {
			return err
		}
	}
	<-done
	res := api.NodeResult{Source: "run", Path: req.Path, Status: "failed"}
	if rs := runResults(s.ws, req.Path); len(rs) > 0 {
		for _, r := range rs {
			if r.Path == req.Path {
				res = r
			}
		}
	}
	return send(&api.RunEvent{Type: "done", Result: &res})
}

func (s *service) Generated(ctx context.Context, req *api.TreeRequest) (*api.GeneratedFiles, error) {
	out := &api.GeneratedFiles{Files: []api.GeneratedFile{}}
	err := s.ws.Analyze(req.Overlays, func(a *Analysis) error {
		if a.Res.Diags.HasErrors() {
			return nil
		}
		in := &gen.Input{Tree: a.Res.Tree, Checker: a.Res.Checker, Dir: s.ws.Root}
		for _, g := range s.ws.Reg.Generators("") {
			// The docs are previewed live, as the spec browser.
			if g.Target == "docs" || g.OnRequest {
				continue
			}
			files, err := g.Generate(ctx, in)
			if err != nil {
				continue
			}
			for _, f := range files {
				out.Files = append(out.Files, api.GeneratedFile{Path: filepath.ToSlash(f.Path), Text: string(f.Content)})
			}
		}
		return nil
	})
	return out, err
}

func (s *service) Lsp(w http.ResponseWriter, r *http.Request) {
	if s.shell.LSP == nil {
		route.WriteError(w, route.Coded(http.StatusServiceUnavailable, "server.NO_LANGUAGE_SERVER", "no language server is installed"))
		return
	}
	serveLSP(w, r, s.shell.LSP, s.ws.Root)
}

// rootURI is the file: URI of the workspace's folder, with a trailing
// slash, that every language-server URI of its files starts with.
func rootURI(root string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(root)}
	s := u.String()
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}

// utf16Offset turns a 0-based line and a 0-based UTF-16 column into a byte
// offset of text, clamped to the line.
func utf16Offset(text string, line, char int) int {
	off := 0
	for i := 0; i < line; i++ {
		j := strings.IndexByte(text[off:], '\n')
		if j < 0 {
			return len(text)
		}
		off += j + 1
	}
	n := 0
	for i, r := range text[off:] {
		if n >= char || r == '\n' {
			return off + i
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return len(text)
}
