package gen

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
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

type viaPointerJSON struct {
	*baseJSON
	Own int `json:"own"`
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

func TestTypes_AFieldMergedThroughAnEmbeddedPointerIsOptional(t *testing.T) {
	m, err := build(mini(reflect.TypeFor[viaPointerJSON]()))
	require.NoError(t, err)
	ts, err := m.types()
	require.NoError(t, err)
	assert.Contains(t, ts, "export interface ViaPointer {\n  kept?: number;\n  own: number;\n}\n",
		"a nil embedded pointer leaves its fields out of the JSON")
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

func TestGenTS_HasTheContractsExports(t *testing.T) {
	m, err := build(sample())
	require.NoError(t, err)
	out, err := m.genTS()
	require.NoError(t, err)
	ts := string(out)

	assert.True(t, strings.HasPrefix(ts, "// Code generated by go generate ./internal/web; DO NOT EDIT.\n"))
	assert.NotContains(t, ts, "import ", "gen.ts has no imports")
	assert.Contains(t, ts, "export type Method = \"GET\" | \"POST\" | \"PUT\" | \"DELETE\";\n")
	for _, want := range []string{
		"  | { kind: \"ok\"; status: number; value: T; notices: Notice[] }\n",
		"  | { kind: \"pending\" }\n",
		"  | { kind: \"unpaired\" }\n",
		"  | { kind: \"refused\"; status: number; message: string }\n",
		"  | { kind: \"stopping\"; message: string }\n",
		"  | { kind: \"timeout\"; message: string }\n",
		"  | { kind: \"failed\"; status: number; message: string };\n",
		"export interface Transport {\n  send<T>(\n    method: Method,\n    path: string,\n    query: Record<string, string> | undefined,\n    body: unknown,\n  ): Promise<Outcome<T>>;\n}\n",
	} {
		assert.Contains(t, ts, want)
	}
	assert.Contains(t, ts, "export interface StreamEvents {\n  notice: Notice;\n  ping: Record<string, never>;\n}\n", "one key per event, in the table's order")
	assert.Contains(t, ts, "export function createClient(t: Transport): Client {\n  return {\n")
	assert.Contains(t, ts, "function queryOf(", "a route with a query needs the helper")
}

func TestGenTS_ClientHasOneMethodPerRoute(t *testing.T) {
	m, err := build(sample())
	require.NoError(t, err)
	out, err := m.genTS()
	require.NoError(t, err)
	ts := string(out)

	for _, want := range []string{
		// the interface
		"  pair(body: PairBody): Promise<Outcome<Pair>>;\n",
		"  item(id: number, query?: Partial<ItemParams>): Promise<Outcome<Item>>;\n",
		"  control(id: number, job: number, action: string): Promise<Outcome<Notices>>;\n",
		"  unpair(): Promise<Outcome<void>>;\n",
		"  viewURL(id: number): string;\n",
		// the implementation
		"    pair: (body) => t.send<Pair>(\"POST\", \"/api/v1/pair\", undefined, body),\n",
		"    item: (id, query) => t.send<Item>(\"GET\", `/api/v1/items/${id}`, queryOf(query), undefined),\n",
		"    control: (id, job, action) => t.send<Notices>(\"POST\", `/api/v1/items/${id}/jobs/${job}/${encodeURIComponent(action)}`, undefined, undefined),\n",
		"    unpair: () => t.send<void>(\"DELETE\", \"/api/v1/device\", undefined, undefined),\n",
		"    viewURL: (id) => `/api/v1/items/${id}/view`,\n",
	} {
		assert.Contains(t, ts, want)
	}
}

func TestGenTS_NeedsANoticeType(t *testing.T) {
	m, err := build(web.ContractSpec{Routes: []web.ContractRoute{
		{Method: "POST", Path: "/api/v1/pair", Client: "pair", OK: 200, Body: reflect.TypeFor[pairBody](), Resp: reflect.TypeFor[pairJSON]()},
	}})
	require.NoError(t, err)
	_, err = m.genTS()
	assert.ErrorContains(t, err, "needs a Notice type")
}

func TestGenTS_RefusesAMalformedRoute(t *testing.T) {
	route := func(r web.ContractRoute) web.ContractSpec {
		c := mini(nil)
		c.Routes = []web.ContractRoute{r}
		return c
	}
	pair := reflect.TypeFor[pairJSON]()
	for _, tc := range []struct {
		name string
		r    web.ContractRoute
		want string
	}{
		{"an unknown method", web.ContractRoute{Method: "PATCH", Path: "/api/v1/x", Client: "x", OK: 200, Resp: pair}, "the method must be GET, POST, PUT or DELETE"},
		{"a client name that is not an identifier", web.ContractRoute{Method: "GET", Path: "/api/v1/x", Client: "get-x", OK: 200, Resp: pair}, "is not a TS identifier"},
		{"a query on a POST", web.ContractRoute{Method: "POST", Path: "/api/v1/x", Client: "x", OK: 200, Query: reflect.TypeFor[itemParams](), Resp: pair}, "only a GET takes a query"},
		{"a body on a GET", web.ContractRoute{Method: "GET", Path: "/api/v1/x", Client: "x", OK: 200, Body: reflect.TypeFor[pairBody](), Resp: pair}, "a GET takes no body"},
		{"a 204 with a response", web.ContractRoute{Method: "DELETE", Path: "/api/v1/x", Client: "x", OK: 204, Resp: pair}, "a 204 has no body"},
		{"a 200 without a response", web.ContractRoute{Method: "GET", Path: "/api/v1/x", Client: "x", OK: 200}, "needs a response type"},
		{"a success status of 202", web.ContractRoute{Method: "GET", Path: "/api/v1/x", Client: "x", OK: 202, Resp: pair}, "must be 200, 201 or 204, not 202"},
		{"a page route with a response", web.ContractRoute{Method: "GET", Path: "/api/v1/x", Client: "x", OK: 200, Page: true, Resp: pair}, "a page route has no query, body or response type"},
		{"a path value called body", web.ContractRoute{Method: "POST", Path: "/api/v1/{body}", Client: "x", OK: 200, Resp: pair}, "a path value cannot be called \"body\""},
		{"a path with a stray brace", web.ContractRoute{Method: "GET", Path: "/api/v1/{id", Client: "x", OK: 200, Resp: pair}, "a path value must be a plain {name}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := build(route(tc.r))
			require.NoError(t, err)
			_, err = m.genTS()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	twice := mini(pair)
	twice.Routes = append(twice.Routes, twice.Routes[0])
	m, err := build(twice)
	require.NoError(t, err)
	_, err = m.genTS()
	assert.ErrorContains(t, err, "is used twice")
}

func TestWebAPI_ListsEveryRouteEventTypeAndUnion(t *testing.T) {
	m, err := build(sample())
	require.NoError(t, err)
	out, err := m.webAPI()
	require.NoError(t, err)
	md := string(out)

	assert.True(t, strings.HasPrefix(md, "# Web API\n"))
	assert.Contains(t, md, "do not edit it by hand")
	assert.Contains(t, md, "### `POST /api/v1/pair`\n\nTrades a code for the cookie.\n\n- Auth: none (open)\n- Success: 200\n- Client: `pair`\n- Body: `PairBody`\n- Response: `Pair`\n")
	assert.Contains(t, md, "### `GET /api/v1/items/{id}`\n\nOne item.\n\n- Auth: the device cookie\n- Success: 200\n- Client: `item`\n- Query: `ItemParams`\n- Response: `Item`\n")
	assert.Contains(t, md, "### `DELETE /api/v1/device`\n\nUnpairs.\n\n- Auth: the device cookie\n- Success: 204\n- Client: `unpair`\n")
	assert.Contains(t, md, "### `GET /api/v1/items/{id}/view`\n\nA page.\n\n- Auth: the device cookie\n- Page: HTML for the app's iframe; the generated client builds its URL as `viewURL`.\n")
	assert.Contains(t, md, "## Stream events\n")
	assert.Contains(t, md, "- `notice`: `Notice`\n- `ping`: `Record<string, never>`\n")
	assert.Contains(t, md, "### `Item`\n\n- `kept`: `number`\n- `id`: `number`\n- `kind`: `Level`\n- `note`: `Badge | null`\n")
	assert.Contains(t, md, "- `extra?`: `string[]`\n")
	assert.Contains(t, md, "- `trio`: `number[]`\n- `pair`: `(Badge | null)[]`\n", "an array is written as T[], never a tuple")
	assert.Contains(t, md, "### `Level`\n\n`\"low\"`, `\"high\"`, `\"\"`\n")
}

func TestGenerate_WritesTheFieldLines(t *testing.T) {
	out, err := Generate(sample(), nil)
	require.NoError(t, err)
	assert.NotEmpty(t, out.GenTS)
	assert.NotEmpty(t, out.WebAPI)
	assert.True(t, sort.StringsAreSorted(out.Fields), "a first run is sorted")
	for _, want := range []string{
		"Item.kept: number",
		"Item.kind: Level",
		"Item.note: Badge | null",
		"Item.extra?: string[]",
		"Item.trio: number[]",
		"Item.pair: (Badge | null)[]",
		"Item.kids?: (Item | null)[]",
		"ItemParams.q: string",
		"Notice.at: string",
		"POST /api/v1/pair body=PairBody resp=Pair ok=200 open",
		"GET /api/v1/items/{id} query=ItemParams resp=Item ok=200",
		"POST /api/v1/items/{id}/jobs/{job}/{action} resp=Notices ok=200",
		"DELETE /api/v1/device ok=204",
		"GET /api/v1/items/{id}/view ok=200 page",
		"event notice data=Notice",
		"event ping data=Record<string, never>",
		"union Level \"low\"",
		"union Level \"\"",
	} {
		assert.Contains(t, out.Fields, want)
	}
}

func TestGenerate_FieldsAppendAndNeverRemove(t *testing.T) {
	first, err := Generate(sample(), nil)
	require.NoError(t, err)

	// A committed line the code no longer produces stays where it was; the
	// changed line is added beside it; the new lines follow, sorted.
	committed := []string{"Zed.gone: string", "Item.kind: string"}
	next, err := Generate(sample(), committed)
	require.NoError(t, err)
	assert.Equal(t, committed, next.Fields[:2])
	assert.Contains(t, next.Fields[2:], "Item.kind: Level")
	assert.NotContains(t, next.Fields[2:], "Item.kind: string", "a committed line is not added twice")
	assert.True(t, sort.StringsAreSorted(next.Fields[2:]))
	assert.Equal(t, []string{"Zed.gone: string", "Item.kind: string"}, Dropped(committed, first.Fields))

	// Generating from its own output adds nothing.
	again, err := Generate(sample(), next.Fields)
	require.NoError(t, err)
	assert.Equal(t, next.Fields, again.Fields)
}

func TestGenerate_NamesTheErrorOfAnUntypeableContract(t *testing.T) {
	_, err := Generate(mini(reflect.TypeFor[chanHolderJSON]()), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate:")
	assert.Contains(t, err.Error(), "chanHolderJSON.c")
}

// webDirOf is internal/web, found from this package's directory and checked by
// the repo's go.mod two levels above it, so a moved test fails rather than
// reading nothing.
func webDirOf(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "..", "..", "go.mod"), "the test is rooted at internal/web")
	return dir
}

// TestContract_GeneratedFilesAreFresh fails when gen.ts, web-api.md or
// api.fields no longer matches the route table, the events and the unions:
// run `go generate ./internal/web` after changing any of them.
func TestContract_GeneratedFilesAreFresh(t *testing.T) {
	tsPath, mdPath, fieldsPath := Files(webDirOf(t))
	committed, err := ReadFields(fieldsPath)
	require.NoError(t, err)
	out, err := Generate(web.Contract(), committed)
	require.NoError(t, err)

	gotTS, err := os.ReadFile(tsPath)
	require.NoError(t, err)
	assert.Equal(t, string(out.GenTS), string(gotTS), "gen.ts is stale: run go generate ./internal/web")
	gotMD, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Equal(t, string(out.WebAPI), string(gotMD), "web-api.md is stale: run go generate ./internal/web")
	gotFields, err := os.ReadFile(fieldsPath)
	require.NoError(t, err)
	assert.Equal(t, string(FormatFields(out.Fields)), string(gotFields), "api.fields lacks lines: run go generate ./internal/web")
}

// TestContract_FieldsOnlyGrow fails when a committed api.fields line is no
// longer produced: a field, route, event or union value was removed or
// retyped. Removing one on purpose means deleting its line by hand, in the
// same commit.
func TestContract_FieldsOnlyGrow(t *testing.T) {
	_, _, fieldsPath := Files(webDirOf(t))
	committed, err := ReadFields(fieldsPath)
	require.NoError(t, err)
	require.NotEmpty(t, committed)
	fresh, err := Generate(web.Contract(), nil)
	require.NoError(t, err)
	assert.Empty(t, Dropped(committed, fresh.Fields),
		"api.fields pins these lines, which the code no longer produces: restore the field, route or value, or delete the line here by hand")
}
