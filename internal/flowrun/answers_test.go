package flowrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckAnswer(t *testing.T) {
	takes := map[string][]string{
		KindNeedsContext:  {"text"},
		KindBlocked:       {"text", WordStop},
		KindDesignFinding: {"text", WordAccept, WordStop},
		KindPremiseDesign: {"text", WordStop},
		KindUnresolved:    {"text", WordAccept, WordStop},
		KindGateFailure:   {"text", WordRetry, WordAccept, WordStop},
		KindAgentFailed:   {"text", WordRetry, WordStop},
		KindTreeMoved:     {WordContinue, WordStop},
		KindDocsStrayed:   {"text", WordAccept, WordStop},
	}
	for kind, ok := range takes {
		for _, answer := range []string{"Use the cache.", WordStop, WordAccept, WordRetry, WordContinue} {
			want := answer
			if answer == "Use the cache." {
				want = "text"
			}
			word, err := CheckAnswer(kind, "  "+answer+"\n")
			if !contains(ok, want) {
				assert.Error(t, err, "%s takes no %q", kind, answer)
				continue
			}
			if assert.NoError(t, err, "%s takes %q", kind, answer) && want != "text" {
				assert.Equal(t, answer, word)
			} else {
				assert.Empty(t, word)
			}
		}
	}
	_, err := CheckAnswer(KindBlocked, " ")
	assert.ErrorContains(t, err, "empty")
	_, err = CheckAnswer("nope", "x")
	assert.ErrorContains(t, err, "unknown question kind")
	_, err = CheckAnswer(KindNeedsContext, "accept")
	assert.ErrorContains(t, err, `doesn't take "accept"; it takes an answer`)
}

func TestTakes(t *testing.T) {
	assert.Equal(t, "an answer", Takes(KindNeedsContext))
	assert.Equal(t, "a decision or stop", Takes(KindBlocked))
	assert.Equal(t, "a decision, retry, accept or stop", Takes(KindGateFailure))
	assert.Equal(t, "continue or stop", Takes(KindTreeMoved))
	assert.Equal(t, "a decision, retry or stop", Takes(KindAgentFailed))
	assert.Equal(t, "a decision, accept or stop", Takes(KindDocsStrayed))
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestStateAnswer_RecordsWhereTheAnswerCameFrom(t *testing.T) {
	st := &State{Status: Parked, Question: &Question{Key: "k", Kind: KindNeedsContext, Text: "Which?"}}
	assert.Error(t, st.Answer("Blue.", "m", "manager", ""), "only spec or owner")
	assert.Equal(t, Parked, st.Status, "a refused answer changes nothing")
	assert.NoError(t, st.Answer(" Blue.\n", "m", FromOwner, ""))
	assert.Equal(t, []Answer{{Question: Question{Key: "k", Kind: KindNeedsContext, Text: "Which?"}, Answer: "Blue.", Session: "m", From: FromOwner}}, st.Answers)
	assert.Equal(t, Running, st.Status)
	assert.Equal(t, []string{"m"}, st.Managers)
	assert.Error(t, st.Answer("again", "m", FromSpec, ""), "no open question")
}
