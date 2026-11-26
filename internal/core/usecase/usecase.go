// Package usecase reads a use case's body: a numbered list is its main
// path, step N the Nth item, and items numbered after a step and a letter,
// as `2a.`, are its extensions, each a condition up to its first `:`, then
// steps, then one outcome: `Fails.`, `Back to N.` or `Succeeds.`.
package usecase

import (
	"regexp"
	"strconv"
	"strings"
)

// Outcome is how an extension ends.
type Outcome int

const (
	NoOutcome Outcome = iota
	Fails
	BackTo
	Succeeds
	Several
)

// Step is one step: its number, as "3" or "2a.1", and its text with its
// offsets in the source.
type Step struct {
	Num        string
	Text       string
	Start, End int
}

// Ext is an extension at a step.
type Ext struct {
	Num        string // as "2a"
	At         int    // the step it branches off
	Cond       string
	Steps      []Step
	Outcome    Outcome
	OutcomeAt  int // offset of the outcome sentence, or -1
	BackTo     int
	Last       string // the last sentence as written
	Start, End int
}

// UseCase is a parsed body.
type UseCase struct {
	Steps []Step
	Exts  []Ext
}

var (
	mainItem = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+`)
	extItem  = regexp.MustCompile(`^(\s*)[-*+]\s+(\d+)([a-z])[.)]\s+`)
	backTo   = regexp.MustCompile(`^Back to (\d+)\.$`)
	prefix   = regexp.MustCompile(`^\s*\|( |$)`)
)

type item struct {
	ext        bool
	num        int
	letter     string
	text       []string
	starts     []int
	start, end int
}

// Parse reads a body whose text starts at offset base in its file.
func Parse(text string, base int) UseCase {
	var items []*item
	var cur *item
	off := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		lineStart := base + off
		off += len(line)
		raw := strings.TrimRight(line, "\r\n")
		body := raw
		if m := prefix.FindStringIndex(body); m != nil {
			body = body[m[1]:]
			lineStart += m[1]
		}
		if t := strings.TrimSpace(body); t == "" || t == "}}" || strings.HasPrefix(t, "</") || strings.HasPrefix(t, "{{") {
			cur = nil
			continue
		}
		if m := extItem.FindStringSubmatchIndex(body); m != nil {
			n, _ := strconv.Atoi(body[m[4]:m[5]])
			cur = &item{ext: true, num: n, letter: body[m[6]:m[7]], start: lineStart}
			items = append(items, cur)
			cur.text = append(cur.text, body[m[1]:])
			cur.starts = append(cur.starts, lineStart+m[1])
			cur.end = lineStart + len(body)
			continue
		}
		if m := mainItem.FindStringSubmatchIndex(body); m != nil && (cur == nil || !cur.ext || m[3]-m[2] == 0) {
			cur = &item{start: lineStart}
			items = append(items, cur)
			cur.text = append(cur.text, body[m[1]:])
			cur.starts = append(cur.starts, lineStart+m[1])
			cur.end = lineStart + len(body)
			continue
		}
		if cur != nil && (strings.HasPrefix(body, " ") || strings.HasPrefix(body, "\t")) {
			t := strings.TrimLeft(body, " \t")
			cur.text = append(cur.text, t)
			cur.starts = append(cur.starts, lineStart+len(body)-len(t))
			cur.end = lineStart + len(body)
			continue
		}
		cur = nil
	}
	var uc UseCase
	n := 0
	for _, it := range items {
		if it.ext {
			continue
		}
		n++
		uc.Steps = append(uc.Steps, Step{Num: strconv.Itoa(n), Text: strings.Join(it.text, " "), Start: it.starts[0], End: it.end})
	}
	for _, it := range items {
		if !it.ext {
			continue
		}
		e := Ext{Num: strconv.Itoa(it.num) + it.letter, At: it.num, Start: it.start, End: it.end, OutcomeAt: -1}
		joined := strings.Join(it.text, " ")
		jstart := it.starts[0]
		// Map an offset of the joined text back to the source.
		at := func(i int) int {
			acc := 0
			for k, t := range it.text {
				if i <= acc+len(t) {
					return it.starts[k] + i - acc
				}
				acc += len(t) + 1
			}
			return it.end
		}
		_ = jstart
		rest, restAt := joined, 0
		if i := strings.Index(joined, ":"); i >= 0 {
			e.Cond = strings.TrimSpace(joined[:i])
			rest, restAt = joined[i+1:], i+1
		}
		sents := sentences(rest)
		k := 0
		for idx, s := range sents {
			st := strings.TrimSpace(rest[s[0]:s[1]])
			if st == "" {
				continue
			}
			if idx == len(sents)-1 {
				e.Last = st
				e.OutcomeAt = at(restAt + s[0] + strings.Index(rest[s[0]:s[1]], st))
				switch {
				case st == "Fails.":
					e.Outcome = Fails
				case st == "Succeeds.":
					e.Outcome = Succeeds
				case backTo.MatchString(st):
					e.Outcome = BackTo
					e.BackTo, _ = strconv.Atoi(backTo.FindStringSubmatch(st)[1])
				}
				if e.Outcome != NoOutcome {
					continue
				}
			}
			k++
			e.Steps = append(e.Steps, Step{Num: e.Num + "." + strconv.Itoa(k), Text: st, Start: at(restAt + s[0]), End: at(restAt + s[1])})
		}
		uc.Exts = append(uc.Exts, e)
	}
	return uc
}

// sentences splits text at ". " ends, keeping links and code spans whole.
func sentences(t string) [][2]int {
	var out [][2]int
	start, depth, tick := 0, 0, false
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '`':
			tick = !tick
		case '[':
			if !tick {
				depth++
			}
		case ']':
			if !tick && depth > 0 {
				depth--
			}
		case '.', '!', '?':
			if !tick && depth == 0 && (i+1 == len(t) || t[i+1] == ' ') {
				out = append(out, [2]int{start, i + 1})
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(t[start:]) != "" {
		out = append(out, [2]int{start, len(t)})
	}
	return out
}

var leadLink = regexp.MustCompile("^\\[(`?)([A-Za-z_][A-Za-z0-9_.]*)`?\\]")

// Lead is the name a step's leading short link names, or "".
func Lead(text string) string {
	if m := leadLink.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return m[2]
	}
	return ""
}

// Include is the use case a step that is only a link names, or "".
func Include(text string) string {
	t := strings.TrimSuffix(strings.TrimSpace(text), ".")
	if m := leadLink.FindStringSubmatch(t); m != nil && len(m[0]) == len(t) {
		return m[2]
	}
	return ""
}
