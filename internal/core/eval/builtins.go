package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// native is a func of the standard packages that Go backs: what can't be
// written in Heron, such as process control. Its arguments are bound to
// its Heron declaration's parameters.
type native func(ctx context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error)

// natives are keyed by the qualified path of their declaration.
var natives map[string]native

func init() {
	natives = map[string]native{
		"core.log":    nativeLog,
		"core.emit":   nativeEmit,
		"process.run": nativeRun,
		"files.read":  nativeRead,
		"files.write": nativeWrite,
		"files.temp":  nativeTemp,
		"json.decode": nativeDecode,
		"json.encode": nativeEncode,
	}
}

func nativeLog(_ context.Context, m *Machine, args map[string]Value, _ *resolve.Node) (Value, error) {
	line := Str(args["message"])
	m.event(Event{Kind: EventLog, Value: line})
	if m.Caps.Log != nil {
		m.Caps.Log(line)
	}
	return nil, nil
}

func nativeEmit(_ context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error) {
	if len(m.emits) == 0 {
		return nil, m.errorf(decl.Decl, nil, "emit(…) adds source only inside a macro")
	}
	out := m.emits[len(m.emits)-1]
	*out = append(*out, Str(args["source"]))
	return nil, nil
}

// abs joins a relative path to the machine's directory.
func (m *Machine) abs(p string) string {
	if p == "" || path.IsAbs(p) || m.Dir == "" {
		return p
	}
	return path.Join(m.Dir, p)
}

func nativeRun(ctx context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error) {
	if m.Caps.Processes == nil {
		return nil, m.errorf(decl.Decl, nil, "no processes can run here")
	}
	var c Command
	switch cmd := args["command"].(type) {
	case Text:
		// A script: the embed's language is the program, its text the
		// script, and inserted values are already quoted.
		c.Path, c.Args = cmd.Lang, []string{"-c", cmd.Text}
		if l, _ := args["args"].([]Value); len(l) > 0 {
			c.Args = append(c.Args, cmd.Lang)
		}
	case string:
		c.Path = cmd
	default:
		return nil, m.errorf(decl.Decl, nil, "run needs a command or a script, got %s", TypeName(cmd))
	}
	if l, ok := args["args"].([]Value); ok {
		for _, a := range l {
			c.Args = append(c.Args, Str(a))
		}
	}
	if m.Caps.Env != nil {
		c.Env = append(c.Env, m.Caps.Env.Environ()...)
	}
	if env, ok := args["env"].(map[string]Value); ok {
		for _, k := range sortedKeys(env) {
			c.Env = append(c.Env, k+"="+Str(env[k]))
		}
	}
	c.Dir = m.Dir
	if cwd, ok := args["cwd"].(string); ok {
		c.Dir = m.abs(cwd)
	}
	switch in := args["stdin"].(type) {
	case nil:
	case *Record:
		c.Stdin = []byte(Str(in.Fields["stdout"]))
	default:
		c.Stdin = []byte(Str(in))
	}
	exit, err := m.Caps.Processes.Run(ctx, c)
	if err != nil {
		return nil, Thrown("ProcessError", map[string]Value{"message": err.Error(), "exit": int64(-1)})
	}
	out := string(exit.Stdout)
	if p, ok := args["stdout"].(string); ok {
		if err := m.writeFile(decl, p, exit.Stdout); err != nil {
			return nil, err
		}
		out = ""
	}
	proc := &Record{Type: "Process", Fields: map[string]Value{
		"exit": int64(exit.Code), "stdout": out, "stderr": string(exit.Stderr),
	}}
	m.event(Event{Kind: EventProcess, Name: c.Path, Args: map[string]Value{"args": strs(c.Args)}, Value: proc})
	if exit.Code != 0 {
		msg := fmt.Sprintf("%s exited with status %d", c.Path, exit.Code)
		if s := strings.TrimSpace(string(exit.Stderr)); s != "" {
			msg += ": " + lastLine(s)
		}
		return nil, Thrown("ProcessError", map[string]Value{"message": msg, "exit": int64(exit.Code)})
	}
	return proc, nil
}

func strs(ss []string) []Value {
	out := make([]Value, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (m *Machine) writeFile(decl *resolve.Node, p string, data []byte) error {
	if m.Caps.Files == nil {
		return m.errorf(decl.Decl, nil, "no files can be written here")
	}
	if err := m.Caps.Files.Write(m.abs(p), data); err != nil {
		return Thrown("FileError", map[string]Value{"message": err.Error(), "path": p})
	}
	return nil
}

func nativeRead(_ context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error) {
	if m.Caps.Files == nil {
		return nil, m.errorf(decl.Decl, nil, "no files can be read here")
	}
	p := Str(args["path"])
	b, err := m.Caps.Files.Read(m.abs(p))
	if err != nil {
		return nil, Thrown("FileError", map[string]Value{"message": err.Error(), "path": p})
	}
	return string(b), nil
}

func nativeWrite(_ context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error) {
	return nil, m.writeFile(decl, Str(args["path"]), []byte(Str(args["content"])))
}

func nativeTemp(_ context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error) {
	if m.Caps.Files == nil {
		return nil, m.errorf(decl.Decl, nil, "no temporary files can be made here")
	}
	if m.tempDir == "" {
		d, err := m.Caps.Files.TempDir()
		if err != nil {
			return nil, err
		}
		m.tempDir = d
	}
	name := path.Base(Str(args["name"]))
	return path.Join(m.tempDir, name), nil
}

func nativeDecode(_ context.Context, m *Machine, args map[string]Value, decl *resolve.Node) (Value, error) {
	dec := json.NewDecoder(strings.NewReader(Str(args["text"])))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, Thrown("DecodeError", map[string]Value{"message": err.Error()})
	}
	t, _ := args["type"].(TypeValue)
	return m.shape(FromPlain(raw), t)
}

// shape gives decoded data the types a TypeValue names: records of
// declared types inside lists and maps.
func (m *Machine) shape(v Value, t TypeValue) (Value, error) {
	if t.Node == nil {
		return v, nil
	}
	switch t.Node.Name {
	case "List", "Set":
		l, ok := v.([]Value)
		if !ok {
			return nil, Thrown("DecodeError", map[string]Value{"message": "expected a list for " + t.Name()})
		}
		if len(t.Args) == 0 {
			return l, nil
		}
		out := make([]Value, len(l))
		for i, x := range l {
			y, err := m.shape(x, t.Args[0])
			if err != nil {
				return nil, err
			}
			out[i] = y
		}
		return out, nil
	case "Map":
		mp, ok := v.(map[string]Value)
		if !ok || len(t.Args) < 2 {
			return v, nil
		}
		out := map[string]Value{}
		for k, x := range mp {
			y, err := m.shape(x, t.Args[1])
			if err != nil {
				return nil, err
			}
			out[k] = y
		}
		return out, nil
	}
	if resolve.IsBuiltin(t.Node) || m.Types.IsEnum(t.Node) {
		return v, nil
	}
	if mp, ok := v.(map[string]Value); ok && m.Types.IsType(t.Node) {
		return &Record{Type: t.Node.Name, Fields: mp}, nil
	}
	return v, nil
}

func nativeEncode(_ context.Context, _ *Machine, args map[string]Value, _ *resolve.Node) (Value, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Plain(args["value"])); err != nil {
		return nil, Thrown("EncodeError", map[string]Value{"message": err.Error()})
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// --- members and methods of values ---

// property reads a built-in member, such as list.size or name.pascal.
func property(v Value, name string) (Value, bool) {
	if t, ok := v.(Text); ok {
		v = t.Text
	}
	switch x := v.(type) {
	case string:
		switch name {
		case "size":
			return int64(len([]rune(x))), true
		case "upper":
			return strings.ToUpper(x), true
		case "lower":
			return strings.ToLower(x), true
		case "trimmed":
			return strings.TrimSpace(x), true
		case "pascal":
			return joinWords(words(x), true, ""), true
		case "camel":
			return joinWords(words(x), false, ""), true
		case "snake":
			return strings.ToLower(strings.Join(words(x), "_")), true
		case "kebab":
			return strings.ToLower(strings.Join(words(x), "-")), true
		case "lines":
			return strs(strings.Split(strings.TrimSuffix(x, "\n"), "\n")), true
		}
	case []Value:
		switch name {
		case "size":
			return int64(len(x)), true
		case "empty":
			return len(x) == 0, true
		case "first":
			if len(x) == 0 {
				return nil, true
			}
			return x[0], true
		case "last":
			if len(x) == 0 {
				return nil, true
			}
			return x[len(x)-1], true
		}
	case map[string]Value:
		switch name {
		case "size":
			return int64(len(x)), true
		case "keys":
			return strs(sortedKeysOf(x)), true
		}
	}
	return nil, false
}

func sortedKeysOf(m map[string]Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// words splits a name into its words: at underscores, hyphens, spaces and
// lower-to-upper changes.
func words(s string) []string {
	var out []string
	var cur []rune
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = nil
			}
			continue
		case unicode.IsUpper(r) && i > 0 && len(cur) > 0 && unicode.IsLower(rs[i-1]):
			out = append(out, string(cur))
			cur = nil
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func joinWords(ws []string, firstUpper bool, sep string) string {
	for i, w := range ws {
		w = strings.ToLower(w)
		if i > 0 || firstUpper {
			rs := []rune(w)
			rs[0] = unicode.ToUpper(rs[0])
			w = string(rs)
		}
		ws[i] = w
	}
	return strings.Join(ws, sep)
}

// builtinMethod calls a built-in method of a value; ok is false when the
// value has none by that name.
func builtinMethod(v Value, name string, args []Value) (Value, bool, error) {
	if t, ok := v.(Text); ok {
		v = t.Text
	}
	arg := func(i int) (Value, error) {
		if i >= len(args) {
			return nil, fmt.Errorf("%s needs %d argument%s", name, i+1, plural(i+1))
		}
		return args[i], nil
	}
	strArg := func(i int) (string, error) {
		a, err := arg(i)
		if err != nil {
			return "", err
		}
		s, ok := a.(string)
		if !ok {
			if t, ok := a.(Text); ok {
				return t.Text, nil
			}
			return "", fmt.Errorf("%s needs a String, got %s", name, TypeName(a))
		}
		return s, nil
	}
	switch x := v.(type) {
	case string:
		switch name {
		case "contains", "starts_with", "ends_with", "split", "trim":
			s, err := strArg(0)
			if err != nil {
				return nil, true, err
			}
			switch name {
			case "contains":
				return strings.Contains(x, s), true, nil
			case "starts_with":
				return strings.HasPrefix(x, s), true, nil
			case "ends_with":
				return strings.HasSuffix(x, s), true, nil
			case "split":
				return strs(strings.Split(x, s)), true, nil
			default:
				return strings.Trim(x, s), true, nil
			}
		case "replace":
			a, err := strArg(0)
			if err != nil {
				return nil, true, err
			}
			b, err := strArg(1)
			if err != nil {
				return nil, true, err
			}
			return strings.ReplaceAll(x, a, b), true, nil
		}
	case []Value:
		switch name {
		case "contains":
			a, err := arg(0)
			if err != nil {
				return nil, true, err
			}
			c, _ := contains(x, a)
			return c, true, nil
		case "join":
			sep := ""
			if len(args) > 0 {
				s, err := strArg(0)
				if err != nil {
					return nil, true, err
				}
				sep = s
			}
			parts := make([]string, len(x))
			for i, it := range x {
				parts[i] = Str(it)
			}
			return strings.Join(parts, sep), true, nil
		case "get":
			a, err := arg(0)
			if err != nil {
				return nil, true, err
			}
			i, ok := a.(int64)
			if !ok {
				return nil, true, fmt.Errorf("get needs an Int, got %s", TypeName(a))
			}
			if i < 0 || int(i) >= len(x) {
				return nil, true, nil
			}
			return x[i], true, nil
		}
	case map[string]Value:
		switch name {
		case "contains", "get":
			k, err := strArg(0)
			if err != nil {
				return nil, true, err
			}
			it, has := x[k]
			if name == "contains" {
				return has, true, nil
			}
			return it, true, nil
		}
	}
	return nil, false, nil
}

// convert is a one-argument call of a builtin type, such as String(42).
func convert(typ string, v Value) (Value, error) {
	switch typ {
	case "String":
		return Str(v), nil
	case "Int":
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			return int64(x), nil
		case string:
			i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				return nil, Thrown("ParseError", map[string]Value{"message": fmt.Sprintf("%q is no Int", x)})
			}
			return i, nil
		}
	case "Float":
		if f, ok := num(v); ok {
			return f, nil
		}
		if s, ok := v.(string); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return nil, Thrown("ParseError", map[string]Value{"message": fmt.Sprintf("%q is no Float", s)})
			}
			return f, nil
		}
	case "Bool":
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case "List", "Set":
		if l, ok := v.([]Value); ok {
			return l, nil
		}
	default:
		return v, nil
	}
	return nil, &Error{Msg: fmt.Sprintf("%s(…) can't convert %s", typ, TypeName(v))}
}
