package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeQuickReplies(t *testing.T) {
	got, err := NormalizeQuickReplies([]string{"  yes ", "ship it", "yes", "/compact"})
	require.NoError(t, err)
	assert.Equal(t, []string{"yes", "ship it", "/compact"}, got, "trimmed, and a duplicate after its first dropped")

	got, err = NormalizeQuickReplies([]string{})
	require.NoError(t, err)
	assert.NotNil(t, got, "an empty list is a list")
	assert.Empty(t, got)

	twelve := make([]string, MaxQuickReplies)
	for i := range twelve {
		twelve[i] = strings.Repeat("a", i+1)
	}
	_, err = NormalizeQuickReplies(twelve)
	assert.NoError(t, err, "twelve is the most")
	_, err = NormalizeQuickReplies([]string{strings.Repeat("é", maxQuickReplyLen)})
	assert.NoError(t, err, "a hundred characters, however many bytes")

	for _, tc := range []struct {
		name    string
		replies []string
		why     string
	}{
		{"an empty reply", []string{"yes", "  "}, "reply 2 is empty"},
		{"a reply too long", []string{strings.Repeat("a", maxQuickReplyLen+1)}, "longer than 100 characters"},
		{"a newline", []string{"one\ntwo"}, "control character"},
		{"a tab", []string{"one\ttwo"}, "control character"},
		{"bytes that are not UTF-8", []string{"\xff"}, "not valid UTF-8"},
		{"thirteen replies", append(twelve, "thirteen"), "13 replies; at most 12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeQuickReplies(tc.replies)
			require.ErrorIs(t, err, ErrQuickReplies)
			assert.Contains(t, err.Error(), tc.why)
		})
	}
}

func TestDefaultQuickReplies_IsAFreshListEachCall(t *testing.T) {
	a := DefaultQuickReplies()
	assert.Equal(t, []string{"yes", "continue", "/compact", "/clear"}, a)
	a[0] = "changed"
	assert.Equal(t, "yes", DefaultQuickReplies()[0], "a caller's edit reaches no one else")
}

// webFileQuickReplies is web.json's quick_replies, read back raw: nil when
// the file leaves them out.
func webFileQuickReplies(t *testing.T) []string {
	t.Helper()
	_, text := webHostFileOf(t)
	var f struct {
		QuickReplies []string `json:"quick_replies"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &f))
	return f.QuickReplies
}

func TestWebHost_SetQuickRepliesStoresTheListAndNilRestoresTheDefaults(t *testing.T) {
	m, _ := webHostModel(t)
	assert.Nil(t, m.Web().QuickReplies, "unset until the user sets some")

	stored, err := m.SetQuickReplies([]string{" ship it ", "yes", "yes"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ship it", "yes"}, stored)
	assert.Equal(t, stored, m.Web().QuickReplies)
	assert.Equal(t, []string{"ship it", "yes"}, webFileQuickReplies(t), "kept in web.json")

	stored, err = m.SetQuickReplies([]string{})
	require.NoError(t, err)
	assert.NotNil(t, stored)
	assert.Empty(t, stored, "no chips is a choice")
	assert.Equal(t, []string{}, webFileQuickReplies(t), "kept as an empty list")

	stored, err = m.SetQuickReplies(nil)
	require.NoError(t, err)
	assert.Nil(t, stored)
	assert.Nil(t, m.Web().QuickReplies, "the defaults again")
	_, text := webHostFileOf(t)
	assert.NotContains(t, text, "quick_replies", "an unset list is left out of web.json")
}

func TestWebHost_SetQuickRepliesRefusesABadListAndChangesNothingWhenTheWriteFails(t *testing.T) {
	m, _ := webHostModel(t)
	_, err := m.SetQuickReplies([]string{"keep"})
	require.NoError(t, err)

	_, err = m.SetQuickReplies([]string{"two\nlines"})
	require.ErrorIs(t, err, ErrQuickReplies)
	assert.Equal(t, []string{"keep"}, m.Web().QuickReplies, "a refused list changes nothing")

	webHostBreakWrites(t)
	_, err = m.SetQuickReplies([]string{"lost"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrQuickReplies, "a failed write is no refusal of the list")
	assert.Equal(t, []string{"keep"}, m.Web().QuickReplies, "the list is put back")
}

func TestWebHost_SetQuickRepliesIsRefusedWhileWebJSONIsLeftAlone(t *testing.T) {
	m := webModel(t, `{not json`)

	_, err := m.SetQuickReplies([]string{"yes"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "left alone")
	assert.NotErrorIs(t, err, ErrQuickReplies)
}

func TestInitWeb_LoadsTheQuickReplies(t *testing.T) {
	m := webModel(t, `{"version":1,"devices":[],"quick_replies":["yes","/compact"]}`)
	assert.Equal(t, []string{"yes", "/compact"}, m.Web().QuickReplies)

	m = webModel(t, `{"version":1,"devices":[],"quick_replies":[]}`)
	assert.NotNil(t, m.Web().QuickReplies, "an empty list stays a list")
	assert.Empty(t, m.Web().QuickReplies)

	m = webModel(t, `{"version":1,"devices":[]}`)
	assert.Nil(t, m.Web().QuickReplies, "a file without them leaves them unset")
}

func TestInitWeb_LeavesAFileWithQuickRepliesItWouldNotWriteAlone(t *testing.T) {
	for _, tc := range []struct{ name, replies, why string }{
		{"a reply with spaces around it", `[" yes"]`, "spaces around it"},
		{"a reply listed twice", `["yes","yes"]`, "listed twice"},
		{"an empty reply", `[""]`, "is empty"},
		{"too many", `["1","2","3","4","5","6","7","8","9","10","11","12","13"]`, "at most 12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"version":1,"devices":[],"quick_replies":` + tc.replies + `}`

			m := webModel(t, body)

			assert.Contains(t, m.web.loadErr, tc.why)
			assert.Equal(t, body, string(webFileBytes(t, m)), "the file is as it was")
		})
	}
}

func TestSaveWeb_RoundTripsTheQuickReplies(t *testing.T) {
	for _, replies := range [][]string{{"yes", "ship it"}, {}} {
		m := webModel(t, webJSON)
		m.web.quickReplies = replies

		require.NoError(t, m.saveWeb())

		m2 := NewForTest(Options{})
		m2.initWeb()
		require.Empty(t, m2.web.loadErr)
		assert.Equal(t, replies, m2.Web().QuickReplies)
	}
}
