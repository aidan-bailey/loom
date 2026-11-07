package flowrun

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestory(t *testing.T) {
	parked := func() *State {
		st := newState(story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"), story("3", "intermediate-dev", "vouched"))
		st.Status, st.Question = Parked, &Question{Key: "ctx:r-s2-dev1:1", Kind: KindNeedsContext}
		st.Memo = map[string]json.RawMessage{"agent:r-s1-dev1": json.RawMessage(`{"status":"DONE"}`)}
		st.Stories[0].Row.Status = "done"
		return st
	}
	t.Run("an edit inside a story moves it and every later story", func(t *testing.T) {
		st := parked()
		memo, rows := clone(st).Memo, []Row{st.Stories[0].Row, st.Stories[1].Row, st.Stories[2].Row}
		fresh := []Story{story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"), story("3", "intermediate-dev", "vouched")}
		fresh[1].End += 34
		fresh[2].Line, fresh[2].End = fresh[2].Line+34, fresh[2].End+34
		fresh[2].Verify, fresh[2].Interfaces, fresh[2].Landmines = "go test ./y", "Exports Y.", []string{"a landmine"}
		fresh[2].Developer, fresh[2].Tier, fresh[2].Review = "senior-dev", "light", "full"
		kept, err := st.Restory(fresh)
		require.NoError(t, err)
		assert.Equal(t, [][2]int{{100, 199}, {200, 333}, {334, 433}}, ranges(st))
		assert.Equal(t, [][2]int{{100, 199}, {200, 299}, {300, 399}}, baseRanges(st), "each story keeps its range at the base")
		s3 := st.Stories[2]
		assert.Equal(t, "go test ./y", s3.Verify)
		assert.Equal(t, "Exports Y.", s3.Interfaces)
		assert.Equal(t, []string{"a landmine"}, s3.Landmines)
		assert.Equal(t, []string{"intermediate-dev", "deep", "vouched"}, []string{s3.Developer, s3.Tier, s3.Review}, "the roles stay as the run started")
		assert.Equal(t, []string{"Story 3's developer, tier and review stay intermediate-dev, deep and vouched (the plan now says senior-dev, light and full)"}, kept)
		assert.Equal(t, memo, st.Memo)
		assert.Equal(t, rows, []Row{st.Stories[0].Row, st.Stories[1].Row, st.Stories[2].Row})
		assert.Equal(t, Parked, st.Status)

		fresh[1].End += 2
		fresh[2].Line, fresh[2].End = fresh[2].Line+2, fresh[2].End+2
		_, err = st.Restory(fresh)
		require.NoError(t, err)
		assert.Equal(t, [][2]int{{100, 199}, {200, 299}, {300, 399}}, baseRanges(st), "a second edit keeps the base's ranges")
	})
	for name, fresh := range map[string][]Story{
		"a story removed":   {story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full")},
		"a story added":     {story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"), story("3", "intermediate-dev", "vouched"), story("4", "junior-dev", "full")},
		"stories reordered": {story("1", "intermediate-dev", "vouched"), story("3", "intermediate-dev", "vouched"), story("2", "junior-dev", "full")},
		"a story renamed":   {story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"), story("5", "intermediate-dev", "vouched")},
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
