package gen

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContract_ReadsTheTablesAndUnions(t *testing.T) {
	c := web.Contract()
	require.NotEmpty(t, c.Routes)
	var open []string
	for _, r := range c.Routes {
		if r.Open {
			open = append(open, r.Method+" "+r.Path)
		}
		assert.NotEmpty(t, r.Client, r.Method+" "+r.Path)
		assert.NotEmpty(t, r.Doc, r.Method+" "+r.Path)
	}
	assert.Equal(t, []string{"POST /api/v1/pair"}, open, "the table's open set")

	var events []string
	for _, e := range c.Events {
		events = append(events, e.Name)
		assert.NotNil(t, e.Data, e.Name)
	}
	assert.Equal(t, []string{"snapshot", "sessions", "workspaces", "usage", "quick_replies", "device", "notice", "ping", "bye"}, events)

	names := map[string]bool{}
	for _, u := range c.Unions {
		names[u.Name] = true
		assert.NotEmpty(t, u.Values, u.Name)
	}
	for _, want := range []string{
		"SessionGroup", "SessionStatus", "StopVerdict", "JobState", "JobControl", "TurnFrom", "ItemKind",
		"StepState", "AgentPageState", "FileStatus", "FileOmitted", "AskKind", "PrState", "PrChecks",
		"FlowStageName", "FlowStageState", "FlowStatus", "FlowCell", "FlowStepKind", "FlowQuestionKind",
	} {
		assert.True(t, names[want], "the contract's unions hold %s", want)
	}
}

func TestFields_ReadFormatAndDrop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.fields")
	require.NoError(t, os.WriteFile(path, []byte("A.x: string\n\nB.y?: number\n"), 0o644))
	got, err := ReadFields(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"A.x: string", "B.y?: number"}, got, "blank lines are not lines")
	assert.Equal(t, "A.x: string\nB.y?: number\n", string(FormatFields(got)))
	assert.Nil(t, FormatFields(nil))

	assert.Equal(t, []string{"A.x: string"}, Dropped(got, []string{"B.y?: number", "A.x: number"}),
		"a committed line the fresh set lacks is dropped, even when only its type changed")
	assert.Empty(t, Dropped(got, []string{"A.x: string", "B.y?: number", "C.z: boolean"}), "a new line drops nothing")

	_, err = ReadFields(filepath.Join(t.TempDir(), "missing"))
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

type levelKind string

type itemID uint64

type badgeJSON struct {
	Label string `json:"label"`
}

type baseJSON struct {
	Kept int `json:"kept"`
}

type itemJSON struct {
	baseJSON
	ID     itemID           `json:"id"`
	Kind   levelKind        `json:"kind"`
	Note   *badgeJSON       `json:"note"`
	Tags   []string         `json:"tags"`
	Extra  []string         `json:"extra,omitempty"`
	Seen   map[itemID][]int `json:"seen"`
	Raw    json.RawMessage  `json:"raw"`
	Any    any              `json:"any"`
	Blob   []byte           `json:"blob"`
	Trio   [3]int           `json:"trio"`
	Pair   [2]*badgeJSON    `json:"pair"`
	Empty  struct{}         `json:"empty"`
	Secret string           `json:"-"`
	At     time.Time        `json:"at"`
	Kids   []*itemJSON      `json:"kids,omitempty"`
	hidden string
}

type itemParams struct {
	Q    string `query:"q"`
	Page string `query:"page"`
}

type noticeJSON struct {
	Text  string    `json:"text"`
	Error bool      `json:"error"`
	At    time.Time `json:"at"`
}

type noticesJSON struct {
	Notices []noticeJSON `json:"notices"`
}

type pairBody struct {
	Code string `json:"code"`
}

type pairJSON struct {
	Device string `json:"device"`
}

// sample is a contract with an open route with a body, a GET with a query, a
// POST with three path values and no body, a 204, a page route, two events
// and a union.
func sample() web.ContractSpec {
	return web.ContractSpec{
		Routes: []web.ContractRoute{
			{Method: "POST", Path: "/api/v1/pair", Open: true, Client: "pair", OK: 200, Doc: "Trades a code for the cookie.", Body: reflect.TypeFor[pairBody](), Resp: reflect.TypeFor[pairJSON]()},
			{Method: "GET", Path: "/api/v1/items/{id}", Client: "item", OK: 200, Doc: "One item.", Query: reflect.TypeFor[itemParams](), Resp: reflect.TypeFor[itemJSON]()},
			{Method: "POST", Path: "/api/v1/items/{id}/jobs/{job}/{action}", Client: "control", OK: 200, Doc: "Controls a job.", Resp: reflect.TypeFor[noticesJSON]()},
			{Method: "DELETE", Path: "/api/v1/device", Client: "unpair", OK: 204, Doc: "Unpairs."},
			{Method: "GET", Path: "/api/v1/items/{id}/view", Client: "view", OK: 200, Doc: "A page.", Page: true},
		},
		Events: []web.ContractEvent{
			{Name: "notice", Data: reflect.TypeFor[noticeJSON]()},
			{Name: "ping", Data: reflect.TypeFor[struct{}]()},
		},
		Unions: map[reflect.Type]web.ContractUnion{
			reflect.TypeFor[levelKind](): {Name: "Level", Values: []string{"low", "high", ""}},
		},
	}
}

// mini is a contract of one GET route answering resp, with the notice event
// every contract needs.
func mini(resp reflect.Type) web.ContractSpec {
	return web.ContractSpec{
		Routes: []web.ContractRoute{{Method: "GET", Path: "/api/v1/x", Client: "x", OK: 200, Resp: resp}},
		Events: []web.ContractEvent{{Name: "notice", Data: reflect.TypeFor[noticeJSON]()}},
	}
}

func TestTypes_FollowEncodingJSON(t *testing.T) {
	_ = itemJSON{hidden: "x"} // an unexported field is no JSON field
	m, err := build(sample())
	require.NoError(t, err)
	ts, err := m.types()
	require.NoError(t, err)

	assert.Contains(t, ts, "export type Level = \"low\" | \"high\" | \"\";\n", "a union lists its values in order")
	assert.Contains(t, ts, "export interface Item {\n"+
		"  kept: number;\n"+ // an embedded struct's fields are flattened
		"  id: number;\n"+ // a named number kind is a number
		"  kind: Level;\n"+ // a typed string is its union
		"  note: Badge | null;\n"+ // a pointer without omitempty is nullable
		"  tags: string[] | null;\n"+
		"  extra?: string[];\n"+ // omitempty is optional
		"  seen: Record<string, number[] | null> | null;\n"+ // map keys are strings, a slice value is nullable
		"  raw: unknown;\n"+
		"  any: unknown;\n"+
		"  blob: string | null;\n"+ // []byte is a string
		"  trio: number[];\n"+ // an array is the slice's T[], never null: a JSON import widens it to T[]
		"  pair: (Badge | null)[];\n"+ // an array's pointer element is nullable, as a slice's is
		"  empty: Record<string, never>;\n"+
		"  at: string;\n"+ // time.Time is a string
		"  kids?: (Item | null)[];\n"+ // a pointer element is nullable
		"}\n")
	assert.NotContains(t, ts, "secret", "a json:\"-\" field is left out")
	assert.NotContains(t, ts, "hidden", "an unexported field is left out")
	assert.Contains(t, ts, "export interface ItemParams {\n  q: string;\n  page: string;\n}\n", "a query type's fields are named by their query tags")
	assert.Contains(t, ts, "export interface Notice {\n  text: string;\n  error: boolean;\n  at: string;\n}\n")

	var order []string
	for _, match := range regexp.MustCompile(`(?m)^export interface (\w+) \{$`).FindAllStringSubmatch(ts, -1) {
		order = append(order, match[1])
	}
	assert.Equal(t, []string{"Badge", "Item", "ItemParams", "Notice", "Notices", "Pair", "PairBody"}, order, "interfaces are sorted by TS name")
}

type unnamedHolderJSON struct {
	Inner struct{ N int } `json:"inner"`
}

type tokenJSON struct {
	Value string `json:"value"`
}

func (tokenJSON) MarshalJSON() ([]byte, error) { return []byte(`"token"`), nil }

type selfHolderJSON struct {
	Tok tokenJSON `json:"tok"`
}

type chanHolderJSON struct {
	C chan int `json:"c"`
}

type badKeyJSON struct {
	M map[float64]int `json:"m"`
}

type twinJSON struct {
	A int `json:"a"`
}

// Twin has the TS name twinJSON has.
type Twin struct {
	B int `json:"b"`
}

type twinHolderJSON struct {
	A twinJSON `json:"a"`
	B Twin     `json:"b"`
}

type queryHolder struct {
	N int `query:"n"`
}

func TestBuild_RefusesWhatItCannotType(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    web.ContractSpec
		want string
	}{
		{"an unnamed struct with fields", mini(reflect.TypeFor[unnamedHolderJSON]()), "an unnamed struct with fields"},
		{"a type that marshals itself", mini(reflect.TypeFor[selfHolderJSON]()), "marshals itself"},
		{"a channel", mini(reflect.TypeFor[chanHolderJSON]()), "has no JSON type"},
		{"a map keyed by a float", mini(reflect.TypeFor[badKeyJSON]()), "map key"},
		{"two types with one TS name", mini(reflect.TypeFor[twinHolderJSON]()), "TS name Twin"},
		{"a query field that is not a string", web.ContractSpec{
			Routes: []web.ContractRoute{{Method: "GET", Path: "/api/v1/x", Client: "x", OK: 200, Query: reflect.TypeFor[queryHolder](), Resp: reflect.TypeFor[pairJSON]()}},
			Events: mini(nil).Events,
		}, "exported string field with a query tag"},
		{"a union on a type that is not a string", web.ContractSpec{
			Unions: map[reflect.Type]web.ContractUnion{reflect.TypeFor[itemID](): {Name: "Id", Values: []string{"1"}}},
		}, "is not a string type"},
		{"a union named like a type", func() web.ContractSpec {
			c := mini(reflect.TypeFor[badgeJSON]())
			c.Unions = map[reflect.Type]web.ContractUnion{reflect.TypeFor[levelKind](): {Name: "Badge", Values: []string{"a"}}}
			return c
		}(), "TS name Badge"},
		{"a union named like gen.ts's own names", func() web.ContractSpec {
			c := mini(reflect.TypeFor[badgeJSON]())
			c.Unions = map[reflect.Type]web.ContractUnion{reflect.TypeFor[levelKind](): {Name: "Outcome", Values: []string{"a"}}}
			return c
		}(), "already gen.ts itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := build(tc.c)
			if err == nil {
				_, err = m.types()
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
