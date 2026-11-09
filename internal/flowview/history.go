package flowview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PastRun is a record of a run (records.md): its repo key and file, the
// plan's stem, its story count, how it ended, when, and what it cost.
type PastRun struct {
	Key     string
	File    string
	Stem    string
	Plan    string
	Stories int
	Ended   string // done, stopped or cancelled; supervised for a record with no Ended line
	Start   time.Time
	End     time.Time
	// USD is the agent-costs comments' total.usd, summed over a run's
	// manager sessions; HasUSD says the record has one.
	USD    float64
	HasUSD bool
	// FinalUSD is its Cost by agent table's -final- rows' USD, summed;
	// HasFinal says the table has such rows.
	FinalUSD   float64
	HasFinal   bool
	Unreadable string
}

var (
	headerLine  = regexp.MustCompile(`^-\s+(?:\*\*)?([A-Za-z ]+):(?:\*\*)?\s*(.*)$`)
	storyCount  = regexp.MustCompile(`(\d+)\s+stor(?:y|ies)\b`)
	startEnd    = regexp.MustCompile(`^([^,\s]+),?(?:\s+(?:\*\*)?End:(?:\*\*)?\s*(\S+))?`)
	costComment = regexp.MustCompile(`<!-- agent-costs (\{.*\}) -->`)
)

// History reads recordsDir/<key>/*.md, the retros dir left out, newest end
// first. A record it can't make sense of is listed with Unreadable set,
// never skipped.
func History(recordsDir string) ([]PastRun, error) {
	keys, err := os.ReadDir(recordsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []PastRun
	for _, k := range keys {
		if !k.IsDir() || k.Name() == "retros" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(recordsDir, k.Name(), "*.md"))
		for _, p := range files {
			out = append(out, readRecord(k.Name(), p))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].End.Equal(out[j].End) {
			return out[i].End.After(out[j].End)
		}
		return out[i].File > out[j].File
	})
	return out, nil
}

// readRecord reads one record.
func readRecord(key, path string) PastRun {
	r := PastRun{Key: key, File: filepath.Base(path), Stem: strings.TrimSuffix(filepath.Base(path), ".md")}
	text, err := readCapped(path, maxPlanBytes)
	if err != nil {
		r.Unreadable = err.Error()
		return r
	}
	ended := ""
	var problems []string
	for _, line := range strings.Split(text, "\n") {
		m := headerLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Plan":
			if r.Plan != "" {
				continue
			}
			r.Plan = strings.Trim(strings.Fields(m[2] + " ")[0], "`")
			if c := storyCount.FindStringSubmatch(m[2]); c != nil {
				r.Stories, _ = strconv.Atoi(c[1])
			}
		case "Start":
			if s := startEnd.FindStringSubmatch(m[2]); s != nil && r.Start.IsZero() {
				r.Start, _ = time.Parse(time.RFC3339, strings.TrimSuffix(s[1], ","))
				if s[2] != "" {
					r.End, _ = time.Parse(time.RFC3339, strings.TrimSuffix(s[2], ","))
				}
			}
		case "Ended":
			if ended == "" {
				ended = strings.Fields(m[2] + " ")[0]
			}
		}
	}
	r.Ended = ended
	if r.Ended == "" {
		r.Ended = "supervised"
	}
	for _, m := range costComment.FindAllStringSubmatch(text, -1) {
		var c struct {
			Total struct {
				USD *float64 `json:"usd"`
			} `json:"total"`
		}
		if err := json.Unmarshal([]byte(m[1]), &c); err != nil || c.Total.USD == nil {
			problems = append(problems, "an agent-costs comment that isn't its JSON")
			continue
		}
		r.USD += *c.Total.USD
		r.HasUSD = true
	}
	r.FinalUSD, r.HasFinal = finalUSD(text)
	if r.Stories == 0 {
		r.Stories = storyRows(text)
	}
	switch {
	case r.Plan == "":
		problems = append([]string{"no Plan line"}, problems...)
	case r.Stories == 0:
		problems = append([]string{"the Plan line gives no story count"}, problems...)
	}
	if len(problems) > 0 {
		r.Unreadable = fmt.Sprintf("%s: %s", r.File, strings.Join(problems, "; "))
	}
	return r
}

// storyRows counts the rows of a record's Stories table, for a record whose
// Plan line gives no story count.
func storyRows(text string) int {
	in, n := false, 0
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "## Stories":
			in = true
		case !in:
		case strings.HasPrefix(t, "#"):
			return n
		case strings.HasPrefix(t, "|"):
			if !strings.HasPrefix(t, "|---") && !strings.HasPrefix(t, "| Story") && !strings.HasPrefix(t, "|:") {
				n++
			}
		}
	}
	return n
}

// finalUSD sums the USD of the Cost by agent table's rows whose Agent cell
// holds -final-, the USD column found by its header.
func finalUSD(text string) (float64, bool) {
	lines := strings.Split(text, "\n")
	in, col, found := false, -1, false
	total := 0.0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case t == "### Cost by agent":
			in, col = true, -1
			continue
		case !in:
			continue
		case strings.HasPrefix(t, "#"):
			return total, found
		case !strings.HasPrefix(t, "|"):
			if col >= 0 && t != "" {
				return total, found
			}
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if col < 0 {
			for i, c := range cells {
				if c == "USD" {
					col = i
				}
			}
			continue
		}
		if col >= len(cells) || !strings.Contains(cells[0], "-final-") {
			continue
		}
		v, err := strconv.ParseFloat(strings.NewReplacer("$", "", ",", "").Replace(cells[col]), 64)
		if err != nil {
			continue
		}
		total += v
		found = true
	}
	return total, found
}
