package prose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/diag"
)

// Action is a code action a prose finding adds beyond its fixes, run as a
// command by the language server.
type Action struct {
	Title   string
	Command string
	Args    []any
}

const (
	cmdAccept = "heron.vale.accept"
	cmdReject = "heron.vale.reject"
)

// Commands lists the commands Actions can name.
func Commands() []string { return []string{cmdAccept, cmdReject} }

var vocabWord = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}'’.-]*$`)

// Actions returns the vocabulary actions for a Vale alert on a word:
// accept it in, or reject it from, each vocabulary the .vale.ini names.
func Actions(d diag.Diagnostic, word string) []Action {
	if !strings.HasPrefix(d.Code, ValeName+".") || !vocabWord.MatchString(word) {
		return nil
	}
	v, ok := configs.Load(d.File)
	if !ok {
		return nil
	}
	cfg := v.(*ValeConfig)
	if cfg.StylesPath == "" {
		return nil
	}
	var out []Action
	for _, voc := range cfg.Vocab {
		out = append(out,
			Action{Title: fmt.Sprintf("Accept %q in vocabulary %s", word, voc), Command: cmdAccept, Args: []any{cfg.Config, voc, word}},
			Action{Title: fmt.Sprintf("Reject %q in vocabulary %s", word, voc), Command: cmdReject, Args: []any{cfg.Config, voc, word}})
	}
	return out
}

// Run runs a vocabulary command: it adds the word to the vocabulary's
// accept.txt or reject.txt and takes it out of the other.
func Run(command string, args []any) error {
	if command != cmdAccept && command != cmdReject {
		return fmt.Errorf("unknown command %s", command)
	}
	if len(args) != 3 {
		return fmt.Errorf("%s takes a configuration, a vocabulary and a word", command)
	}
	config, _ := args[0].(string)
	voc, _ := args[1].(string)
	word, _ := args[2].(string)
	if !vocabWord.MatchString(word) {
		return fmt.Errorf("%q isn't a word", word)
	}
	cfg := &ValeConfig{Config: config}
	if _, err := os.Stat(config); err != nil || filepath.Base(config) != ".vale.ini" {
		return fmt.Errorf("no Vale configuration %s", config)
	}
	cfg.readIni()
	known := false
	for _, v := range cfg.Vocab {
		known = known || v == voc
	}
	if !known || cfg.StylesPath == "" {
		return fmt.Errorf("%s names no vocabulary %s", config, voc)
	}
	dir := filepath.Join(cfg.StylesPath, "config", "vocabularies", voc)
	add, drop := "accept.txt", "reject.txt"
	if command == cmdReject {
		add, drop = drop, add
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := editList(filepath.Join(dir, drop), word, false); err != nil {
		return err
	}
	return editList(filepath.Join(dir, add), word, true)
}

// editList adds a word to a vocabulary list, or removes it, keeping the
// other lines as they are.
func editList(path, word string, add bool) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) && !add {
		return nil
	}
	var lines []string
	has := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.TrimSpace(l) == word {
			has = true
			if !add {
				continue
			}
		}
		lines = append(lines, l)
	}
	if add && !has {
		lines = append(lines, word)
	}
	if !add && !has {
		return nil
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
