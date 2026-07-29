// Package review holds the comment model and on-disk store for the
// workbench review tab. Derived from kevindutra/crit (see NOTICE.md);
// unlike upstream, every path is rooted at an explicit worktree root
// rather than the process working directory.
package review

import "time"

type Comment struct {
	ID             string    `json:"id" yaml:"id"`
	Line           int       `json:"line" yaml:"line"`
	EndLine        int       `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	ContentSnippet string    `json:"content_snippet" yaml:"content_snippet"`
	Body           string    `json:"body" yaml:"body"`
	CreatedAt      time.Time `json:"created_at" yaml:"created_at"`
}

type ReviewState struct {
	File     string    `json:"file" yaml:"file"`
	Comments []Comment `json:"comments" yaml:"comments"`
}

func (s *ReviewState) AddComment(c Comment) {
	s.Comments = append(s.Comments, c)
}

func (s *ReviewState) DeleteComment(id string) {
	for i, c := range s.Comments {
		if c.ID == id {
			s.Comments = append(s.Comments[:i], s.Comments[i+1:]...)
			return
		}
	}
}
