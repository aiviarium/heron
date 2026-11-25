package lsp

import (
	"encoding/json"
	"sort"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/tools/prose"
)

// codeAction offers each fix of the diagnostics in the range as a quick
// fix, and for prose alerts the actions their provider adds, such as
// accepting or rejecting a word in the project's Vale vocabulary.
func (s *Session) codeAction(params json.RawMessage) (any, error) {
	var p CodeActionParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.TextDocument.URI)
	src, _ := s.text(id)
	l := newLines(src)
	from, to := l.offset(p.Range.Start), l.offset(p.Range.End)
	out := []CodeAction{}
	for _, d := range s.diags[id] {
		if d.End < from || d.Start > to {
			continue
		}
		ld := s.toDiagnostic(l, d)
		for i, fx := range d.Fixes {
			edit := WorkspaceEdit{Changes: map[string][]TextEdit{}}
			for _, e := range fx.Edits {
				loc := s.location(resolve.FileID(e.File), e.Start, e.End)
				edit.Changes[loc.URI] = append(edit.Changes[loc.URI], TextEdit{Range: loc.Range, NewText: e.New})
			}
			out = append(out, CodeAction{Title: fx.Title, Kind: "quickfix", Diagnostics: []Diagnostic{ld}, IsPreferred: i == 0 && !fx.Suggestion, Edit: &edit})
		}
		word := ""
		if d.Start >= 0 && d.End <= len(src) && d.Start < d.End {
			word = src[d.Start:d.End]
		}
		for _, a := range prose.Actions(d, word) {
			out = append(out, CodeAction{Title: a.Title, Kind: "quickfix", Diagnostics: []Diagnostic{ld},
				Command: &Command{Title: a.Title, Command: a.Command, Arguments: a.Args}})
		}
	}
	return out, nil
}

// cmdSync does nothing: like every request it rechecks first, so a client
// that waits for its reply has every diagnostic published so far.
const cmdSync = "heron.sync"

func commandNames() []string {
	names := append([]string{cmdSync}, prose.Commands()...)
	sort.Strings(names)
	return names
}

// executeCommand runs a command a code action named, then rechecks, so a
// word added to a vocabulary clears its alerts.
func (s *Session) executeCommand(params json.RawMessage) (any, error) {
	var p ExecuteCommandParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	if p.Command == cmdSync {
		return nil, nil // the request has rechecked already
	}
	if err := prose.Run(p.Command, p.Arguments); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	s.dirty = true
	s.flush()
	return nil, nil
}
