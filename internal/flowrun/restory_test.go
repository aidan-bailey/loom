package flowrun

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestory(t *testing.T) {
	parked := func() *State {
		st := newState(story("1", "intermediate-dev"), story("2", "junior-dev"), story("3", "intermediate-dev"))
		st.Status, st.Question = Parked, &Question{Key: "ctx:r-s2-dev1:1", Kind: KindNeedsContext}
		st.Memo = map[string]json.RawMessage{"agent:r-s1-dev1": json.RawMessage(`{"status":"DONE"}`)}
		st.Stories[0].Row.Status = "done"
		return st
	}
	t.Run("an edit inside a story moves it and every later story", func(t *testing.T) {
		st := parked()
		memo, rows := clone(st).Memo, []Row{st.Stories[0].Row, st.Stories[1].Row, st.Stories[2].Row}
		fresh := []Story{story("1", "intermediate-dev"), story("2", "junior-dev"), story("3", "intermediate-dev")}
		fresh[1].End += 34
		fresh[2].Line, fresh[2].End = fresh[2].Line+34, fresh[2].End+34
		fresh[2].Verify, fresh[2].Interfaces, fresh[2].Landmines = "go test ./y", "Exports Y.", []string{"a landmine"}
		fresh[2].Files = []string{"y/y.go"}
		fresh[2].Developer, fresh[2].Tier = "senior-dev", "light"
		kept, err := st.Restory(fresh)
		require.NoError(t, err)
		assert.Equal(t, [][2]int{{100, 199}, {200, 333}, {334, 433}}, ranges(st))
		assert.Equal(t, [][2]int{{100, 199}, {200, 299}, {300, 399}}, baseRanges(st), "each story keeps its range at the base")
		s3 := st.Stories[2]
		assert.Equal(t, "go test ./y", s3.Verify)
		assert.Equal(t, "Exports Y.", s3.Interfaces)
		assert.Equal(t, []string{"a landmine"}, s3.Landmines)
		assert.Equal(t, []string{"y/y.go"}, s3.Files, "a plan edit moves the Files line too")
		assert.Equal(t, []string{"intermediate-dev", "deep"}, []string{s3.Developer, s3.Tier}, "the roles stay as the run started")
		assert.Equal(t, []string{"Story 3's developer and tier stay intermediate-dev and deep (the plan now says senior-dev and light)"}, kept)
		assert.Equal(t, memo, st.Memo)
		assert.Equal(t, rows, []Row{st.Stories[0].Row, st.Stories[1].Row, st.Stories[2].Row})
		assert.Equal(t, Parked, st.Status)

		fresh[1].End += 2
		fresh[2].Line, fresh[2].End = fresh[2].Line+2, fresh[2].End+2
		_, err = st.Restory(fresh)
		require.NoError(t, err)
		assert.Equal(t, [][2]int{{100, 199}, {200, 299}, {300, 399}}, baseRanges(st), "a second edit keeps the base's ranges")
	})
	t.Run("a plan that changes a story's form keeps the run's", func(t *testing.T) {
		st := parked()
		fresh := []Story{story("1", "intermediate-dev"), story("2", "junior-dev"), story("3", "intermediate-dev")}
		fresh[1].Form = "mechanical"
		kept, err := st.Restory(fresh)
		require.NoError(t, err)
		assert.Equal(t, "", st.Stories[1].Form, "the form stays as the run started")
		assert.Equal(t, []string{"Story 2's form stays full (the plan now says mechanical)"}, kept)
	})
	t.Run("a plan that pairs differently keeps the run's lanes", func(t *testing.T) {
		st := parked()
		fresh := []Story{story("1", "intermediate-dev"), laned(story("2", "junior-dev")), story("3", "intermediate-dev")}
		kept, err := st.Restory(fresh)
		require.NoError(t, err)
		assert.False(t, st.Stories[1].Lane)
		assert.Equal(t, []string{"Story 2 runs in order, as the run started (the plan now says it runs its developer in a lane)"}, kept)
	})
	for name, fresh := range map[string][]Story{
		"a story removed":   {story("1", "intermediate-dev"), story("2", "junior-dev")},
		"a story added":     {story("1", "intermediate-dev"), story("2", "junior-dev"), story("3", "intermediate-dev"), story("4", "junior-dev")},
		"stories reordered": {story("1", "intermediate-dev"), story("3", "intermediate-dev"), story("2", "junior-dev")},
		"a story renamed":   {story("1", "intermediate-dev"), story("2", "junior-dev"), story("5", "intermediate-dev")},
	} {
		t.Run(name+" is refused and changes nothing", func(t *testing.T) {
			st := parked()
			before := clone(st)
			_, err := st.Restory(fresh)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "stories 1, 2, 3")
			assert.Equal(t, before, st)
		})
	}
}

func ranges(st *State) [][2]int {
	var out [][2]int
	for _, s := range st.Stories {
		out = append(out, [2]int{s.Line, s.End})
	}
	return out
}

func baseRanges(st *State) [][2]int {
	var out [][2]int
	for _, s := range st.Stories {
		l, e := s.baseRange()
		out = append(out, [2]int{l, e})
	}
	return out
}
