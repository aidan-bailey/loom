package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON_AnswersTheStatusAndTheBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]any{"id": 7, "notices": []string{}})
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id": 7, "notices": []}`, rec.Body.String())
}

func TestWriteJSON_AValueThatWillNotEncodeIsA500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"c": make(chan int)})
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "never a 200 with half a body")
	assert.JSONEq(t, `{"error": "the answer could not be encoded"}`, rec.Body.String())
}

func TestWriteError_IsAnErrorObject(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusConflict, "the session is already paused")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error": "the session is already paused"}`, rec.Body.String())
}

func TestReadJSON(t *testing.T) {
	type body struct {
		Code string `json:"code"`
	}
	for _, tc := range []struct {
		name  string
		body  string
		limit int64  // a cap on the body, as guard sets; 0 for none
		want  string // part of the error, or "" for none
	}{
		{"one object", `{"code": "K7QM"}`, 0, ""},
		{"white space after it", "{\"code\": \"K7QM\"}\n  \n", 0, ""},
		{"a field the route does not take", `{"code": "K7QM", "extra": 1}`, 0, `unknown field "extra"`},
		{"nothing", ``, 0, "the body is empty"},
		{"only white space", " \n", 0, "the body is empty"},
		{"two objects", `{"code": "A"} {"code": "B"}`, 0, "more than one JSON value"},
		{"junk after the object", `{"code": "A"} x`, 0, "more than one JSON value"},
		{"not JSON", `not json`, 0, "the body is not the JSON this route takes"},
		{"truncated", `{"code": `, 0, "the body is not the JSON this route takes"},
		{"the wrong type", `{"code": 7}`, 0, "the body is not the JSON this route takes"},
		{"over the cap", `{"code": "K7QM-2XPA-and-more"}`, 16, "the body is over 16 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body))
			if tc.limit > 0 {
				req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, tc.limit)
			}
			var got body
			err := readJSON(req, &got)
			if tc.want == "" {
				require.NoError(t, err)
				assert.Equal(t, "K7QM", got.Code)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
