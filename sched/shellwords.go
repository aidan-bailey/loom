package sched

import (
	"errors"
	"strings"
)

// errScript is why a script has no family (family sizing spec §5.1): it
// holds something the lexer doesn't read.
var errScript = errors.New("a script the family lexer doesn't read")

// scriptKeywords are the words that make a script one the lexer doesn't
// read when they stand in command position: control flow.
var scriptKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true, "while": true,
	"until": true, "do": true, "done": true, "case": true, "esac": true, "function": true,
}

// splitScript is script's simple commands, each as its words (family
// sizing spec §5.1): split on unquoted &&, ||, ;, |, |&, & and newlines;
// quotes and backslashes honoured and removed; $(…), ${…} and backquotes
// kept in their word as written; redirections dropped with their target;
// comments dropped. It fails with errScript on an unbalanced quote, $(…),
// ${…} or backquote, a subshell or brace group, a here-document or
// here-string, a redirection with no target, and control flow.
func splitScript(script string) ([][]string, error) {
	l := lexer{src: script}
	if err := l.run(); err != nil {
		return nil, err
	}
	for _, words := range l.cmds {
		i := 0
		for i < len(words) && isAssignment(words[i]) {
			i++
		}
		if i < len(words) && scriptKeywords[words[i]] {
			return nil, errScript
		}
	}
	return l.cmds, nil
}

// lexer is splitScript's state.
type lexer struct {
	src    string
	i      int
	cur    strings.Builder
	inWord bool // a word has begun, an empty quoted one too
	quoted bool // some of the word was quoted or escaped
	skip   bool // the next word is a redirection's target
	words  []string
	cmds   [][]string
}

func (l *lexer) run() error {
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.endWord()
			l.i++
		case c == '\n':
			if err := l.endCommand(); err != nil {
				return err
			}
			l.i++
		case c == '#' && !l.inWord:
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.i++
			}
		case c == '\'':
			end := strings.IndexByte(l.src[l.i+1:], '\'')
			if end < 0 {
				return errScript
			}
			l.cur.WriteString(l.src[l.i+1 : l.i+1+end])
			l.inWord, l.quoted = true, true
			l.i += end + 2
		case c == '"':
			if err := l.double(); err != nil {
				return err
			}
		case c == '\\':
			if l.i+1 < len(l.src) && l.src[l.i+1] != '\n' { // a backslash-newline joins the lines
				l.cur.WriteByte(l.src[l.i+1])
				l.inWord, l.quoted = true, true
			}
			l.i += 2
		case c == '$' && (l.peek(1) == '(' || l.peek(1) == '{'):
			if err := l.raw(); err != nil {
				return err
			}
		case c == '`':
			if err := l.backquote(); err != nil {
				return err
			}
		case c == '(' || c == ')' || c == '{' || c == '}':
			return errScript
		case c == '<' && l.peek(1) == '<':
			return errScript
		case c == '<' || c == '>' || (c == '&' && l.peek(1) == '>'):
			l.redirect()
		case c == '&' || c == '|' || c == ';':
			if err := l.endCommand(); err != nil {
				return err
			}
			l.i++
			if n := l.peek(0); n == c || (c == '|' && n == '&') {
				l.i++
			}
		default:
			l.cur.WriteByte(c)
			l.inWord = true
			l.i++
		}
	}
	return l.endCommand()
}

// peek is the byte k past l.i, 0 past the end.
func (l *lexer) peek(k int) byte {
	if l.i+k < len(l.src) {
		return l.src[l.i+k]
	}
	return 0
}

// double reads the double-quoted part at l.i: a backslash escapes $, `,
// ", \ and a newline; $(…), ${…} and backquotes stay as written.
func (l *lexer) double() error {
	l.inWord, l.quoted = true, true
	l.i++
	for l.i < len(l.src) {
		switch c := l.src[l.i]; {
		case c == '"':
			l.i++
			return nil
		case c == '\\' && strings.IndexByte("$`\"\\\n", l.peek(1)) >= 0 && l.peek(1) != 0:
			if l.peek(1) != '\n' {
				l.cur.WriteByte(l.peek(1))
			}
			l.i += 2
		case c == '$' && (l.peek(1) == '(' || l.peek(1) == '{'):
			if err := l.raw(); err != nil {
				return err
			}
		case c == '`':
			if err := l.backquote(); err != nil {
				return err
			}
		default:
			l.cur.WriteByte(c)
			l.i++
		}
	}
	return errScript
}

// raw copies the $(…) or ${…} at l.i into the word as written, counting
// nested pairs and skipping quoted parts inside it.
func (l *lexer) raw() error {
	open := l.src[l.i+1]
	closing := byte(')')
	if open == '{' {
		closing = '}'
	}
	depth := 0
	for j := l.i + 1; j < len(l.src); j++ {
		switch l.src[j] {
		case '\\':
			j++
		case '\'', '"':
			end := strings.IndexByte(l.src[j+1:], l.src[j])
			if end < 0 {
				return errScript
			}
			j += end + 1
		case open:
			depth++
		case closing:
			if depth--; depth == 0 {
				l.cur.WriteString(l.src[l.i : j+1])
				l.inWord = true
				l.i = j + 1
				return nil
			}
		}
	}
	return errScript
}

// backquote copies the backquoted part at l.i into the word as written.
func (l *lexer) backquote() error {
	end := strings.IndexByte(l.src[l.i+1:], '`')
	if end < 0 {
		return errScript
	}
	l.cur.WriteString(l.src[l.i : l.i+2+end])
	l.inWord = true
	l.i += end + 2
	return nil
}

// redirect drops the redirection at l.i: an unquoted word of digits right
// before it is its file descriptor, and the word after it its target. The
// operators: <, >, >>, <>, <&, >&, >|, &>, &>>.
func (l *lexer) redirect() {
	if l.inWord && !l.quoted && isDigits(l.cur.String()) {
		l.cur.Reset()
		l.inWord = false
	} else {
		l.endWord()
	}
	if l.src[l.i] == '&' {
		l.i++
	}
	l.i++ // < or >
	if n := l.peek(0); n == '>' || n == '&' || n == '|' {
		l.i++
	}
	l.skip = true
}

func (l *lexer) endWord() {
	if !l.inWord {
		return
	}
	if l.skip {
		l.skip = false
	} else {
		l.words = append(l.words, l.cur.String())
	}
	l.cur.Reset()
	l.inWord, l.quoted = false, false
}

func (l *lexer) endCommand() error {
	l.endWord()
	if l.skip {
		return errScript // a redirection with no target
	}
	if len(l.words) > 0 {
		l.cmds = append(l.cmds, l.words)
		l.words = nil
	}
	return nil
}

// isDigits reports whether s is a non-empty run of decimal digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
