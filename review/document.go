package review

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

type Document struct {
	Path    string
	Content string
	Lines   []string
	Hash    string
}

func LoadDocument(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading document: %w", err)
	}
	content := string(data)
	return &Document{
		Path:    path,
		Content: content,
		Lines:   strings.Split(content, "\n"),
		Hash:    fmt.Sprintf("%x", sha256.Sum256(data)),
	}, nil
}

func (d *Document) LineAt(n int) string {
	if n < 1 || n > len(d.Lines) {
		return ""
	}
	return d.Lines[n-1]
}

func (d *Document) LineCount() int {
	return len(d.Lines)
}
