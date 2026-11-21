package main

import (
	"bufio"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBrowser answers CDP calls on a pair of pipes as the browser would.
func fakeBrowser(t *testing.T, answer func(m cdpMessage) []cdpMessage) (*cdpClient, *[]string, func()) {
	toR, toW := io.Pipe()
	fromR, fromW := io.Pipe()
	var events []string
	c := newCDPClient(fromR, toW, func(method, session string, _ json.RawMessage) {
		events = append(events, session+":"+method)
	})
	go func() {
		r := bufio.NewReader(toR)
		for {
			frame, err := r.ReadBytes(0)
			if err != nil {
				return
			}
			var m cdpMessage
			if json.Unmarshal(frame[:len(frame)-1], &m) != nil {
				return
			}
			for _, out := range answer(m) {
				b, _ := json.Marshal(out)
				if _, err := fromW.Write(append(b, 0)); err != nil {
					return
				}
			}
		}
	}()
	return c, &events, func() { _ = fromW.Close(); _ = toR.Close() }
}

func TestCDP_ACallGetsItsOwnReplyAndEventsGoToTheHandler(t *testing.T) {
	c, events, stop := fakeBrowser(t, func(m cdpMessage) []cdpMessage {
		res, _ := json.Marshal(map[string]string{"echo": m.Method + "@" + m.SessionID})
		return []cdpMessage{
			{Method: "Page.loadEventFired", SessionID: m.SessionID},
			{ID: m.ID, Result: res},
		}
	})
	defer stop()
	var out struct{ Echo string }
	require.NoError(t, c.call(context.Background(), "s1", "Page.navigate", map[string]string{"url": "x"}, &out))
	assert.Equal(t, "Page.navigate@s1", out.Echo)
	assert.Equal(t, []string{"s1:Page.loadEventFired"}, *events, "the event ran before the reply it came with")
}

func TestCDP_AnErrorReplyIsAnError(t *testing.T) {
	c, _, stop := fakeBrowser(t, func(m cdpMessage) []cdpMessage {
		return []cdpMessage{{ID: m.ID, Error: &cdpError{Code: -32601, Message: "no such method"}}}
	})
	defer stop()
	err := c.call(context.Background(), "", "Nope.nope", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Nope.nope: no such method")
}

func TestCDP_ABrowserThatGoesEndsEveryCall(t *testing.T) {
	c, _, stop := fakeBrowser(t, func(cdpMessage) []cdpMessage { return nil })
	errs := make(chan error, 1)
	go func() { errs <- c.call(context.Background(), "", "Browser.getVersion", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	stop()
	select {
	case err := <-errs:
		require.Error(t, err, "the waiting call ends")
	case <-time.After(2 * time.Second):
		t.Fatal("a call still waits on a browser that is gone")
	}
	require.Error(t, c.call(context.Background(), "", "Browser.getVersion", nil, nil), "and so does every later one")
}

func TestFrame_DrawsTheDeviceOverTheViewport(t *testing.T) {
	d := devices[defaultDevice]
	w, h := int(float64(d.width)*d.scale), int(float64(d.height)*d.scale)
	require.Equal(t, [2]int{1179, 2556}, [2]int{w, h}, "the iPhone 15's screen in pixels")
	shot := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range shot.Pix {
		shot.Pix[i] = 128 // mid grey
		if i%4 == 3 {
			shot.Pix[i] = 255 // opaque
		}
	}
	px := func(img *image.NRGBA, x, y float64) color.NRGBA { return img.NRGBAAt(int(x), int(y)) }

	bare := frame(shot, nil, d, frameOpts{})
	assert.Equal(t, shot.Bounds(), bare.Bounds(), "bare: the viewport's size")
	assert.Equal(t, color.NRGBA{0, 0, 0, 255}, px(bare, float64(w)/2, (d.island.y+d.island.h/2)*d.scale), "the Dynamic Island is black")
	home := px(bare, float64(w)/2, (d.homeBar.y+d.homeBar.h/2)*d.scale)
	assert.Greater(t, home.R, uint8(200), "the home indicator is light")
	assert.Equal(t, color.NRGBA{128, 128, 128, 255}, px(bare, 2, 2), "bare keeps the corners")
	assert.Equal(t, color.NRGBA{128, 128, 128, 255}, px(bare, float64(w)/2, float64(h)/2), "the content is untouched")

	pad := bezelPt * d.scale
	framed := frame(shot, nil, d, frameOpts{bezel: true, guides: true})
	assert.Equal(t, image.Rect(0, 0, w+2*int(pad), h+2*int(pad)), framed.Bounds())
	assert.Equal(t, uint8(0), px(framed, 1, 1).A, "outside the device is transparent")
	// The screen's corner and the bezel's share a centre; halfway between
	// their radii, on the diagonal, is bezel.
	centre := pad + d.corner*d.scale
	ring := (d.corner*d.scale + centre) / 2
	assert.Equal(t, inkBezel, px(framed, centre-ring/math.Sqrt2, centre-ring/math.Sqrt2), "the screen's corner is cut to the bezel, guides and all")
	inTop := px(framed, pad+float64(w)/2, pad+float64(d.insetTop)*d.scale-3)
	assert.Greater(t, inTop.R, inTop.G, "the top inset is tinted")
	below := px(framed, pad+float64(w)/2, pad+float64(d.insetTop)*d.scale+3)
	assert.Equal(t, color.NRGBA{128, 128, 128, 255}, below, "the safe area is not")
}

func TestFrame_LaysTheStatusBarOverTheScreen(t *testing.T) {
	d := devices[defaultDevice]
	w, h := int(float64(d.width)*d.scale), int(float64(d.height)*d.scale)
	shot := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range shot.Pix {
		shot.Pix[i] = 255 // opaque white
	}
	bar := image.NewNRGBA(image.Rect(0, 0, w, h)) // transparent
	bar.SetNRGBA(100, 100, color.NRGBA{255, 0, 0, 255})

	out := frame(shot, bar, d, frameOpts{bezel: true})
	pad := int(bezelPt * d.scale)
	assert.Equal(t, color.NRGBA{255, 0, 0, 255}, out.NRGBAAt(pad+100, pad+100), "the bar's pixel, at the screen's origin")
	assert.Equal(t, color.NRGBA{255, 255, 255, 255}, out.NRGBAAt(pad+101, pad+100), "a transparent pixel leaves the screen")
}

func TestDevices_TheDesktopsAreBareViewportsAndThePhonesAreNot(t *testing.T) {
	for name, size := range map[string][2]int{"desktop": {1440, 900}, "desktop-narrow": {1100, 800}} {
		d, ok := devices[name]
		require.True(t, ok, name)
		assert.Equal(t, size, [2]int{d.width, d.height}, name)
		assert.Equal(t, 1.0, d.scale, name)
		assert.True(t, d.desktop, name)
		assert.Zero(t, d.insetTop, name)
		assert.Zero(t, d.insetBottom, name)
		assert.Zero(t, d.corner, name)
		assert.Equal(t, rectPt{}, d.island, name)
		assert.Equal(t, rectPt{}, d.homeBar, name)
		assert.Contains(t, d.userAgent, "Chrome/", name)
		assert.Contains(t, d.userAgent, "X11; Linux x86_64", name)
		assert.NotContains(t, d.userAgent, "Mobile", name)
		assert.NotContains(t, d.userAgent, "iPhone", name)
	}
	// The thresholds are DESK2_MIN 1024 and DESK3_MIN 1280 of app/viewport.ts:
	// the narrow desktop gets the sidebar without the inspector, the wide one both.
	assert.GreaterOrEqual(t, devices["desktop-narrow"].width, 1024)
	assert.Less(t, devices["desktop-narrow"].width, 1280)
	assert.GreaterOrEqual(t, devices["desktop"].width, 1280)
	for _, name := range []string{"iphone15", "iphone16"} {
		assert.False(t, devices[name].desktop, name)
		assert.Equal(t, 3.0, devices[name].scale, name)
	}
}

func TestFrame_ADesktopScreenshotComesBackUnchanged(t *testing.T) {
	d := devices["desktop"]
	shot := image.NewNRGBA(image.Rect(0, 0, d.width, d.height))
	for i := range shot.Pix {
		shot.Pix[i] = 128
		if i%4 == 3 {
			shot.Pix[i] = 255
		}
	}
	shot.SetNRGBA(7, 9, color.NRGBA{10, 200, 30, 255})
	bar := image.NewNRGBA(image.Rect(0, 0, d.width, d.height))
	bar.SetNRGBA(100, 100, color.NRGBA{255, 0, 0, 255})

	out := frame(shot, bar, d, frameOpts{bezel: true, guides: true})
	assert.Equal(t, shot.Bounds(), out.Bounds())
	assert.Equal(t, shot.Pix, out.Pix, "no bezel, bar, island or home indicator")
	out.SetNRGBA(0, 0, color.NRGBA{1, 2, 3, 4})
	assert.Equal(t, color.NRGBA{128, 128, 128, 255}, shot.NRGBAAt(0, 0), "the result is a copy")
}

func TestStatusBarHTML_SitsOnTheIslandsMiddle(t *testing.T) {
	d := devices[defaultDevice]
	page := statusBarHTML(d)
	assert.Contains(t, page, "top: 11px; height: 37px;", "both sides span the island's height")
	assert.Contains(t, page, "width: 133.5px;", "the time is centred in the space left of the island")
	assert.Contains(t, page, "left: 259.5px;", "the icons in the space right of it")
	assert.Contains(t, page, ">9:41<")
}

func TestFillTimes_CountsFromNow(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	v, err := fillTimes(map[string]any{
		"a": "now", "b": []any{"now-2m", "now+1h40m"}, "c": "nowhere", "d": 3.0,
	}, now)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"a": "now", "b": []any{"2026-11-03T09:58:00Z", "2026-11-03T11:40:00Z"}, "c": "nowhere", "d": 3.0,
	}, v)
	_, err = fillTimes("now-2x", now)
	assert.Error(t, err, "a duration that does not parse fails, never passes through")
}

func cannedForTest(t *testing.T) (*cannedLoom, *httptest.Server) {
	static := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(static, "index.html"), []byte(`<meta name="loom-build" content="__LOOM_BUILD__">`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(static, "secret.json"), []byte("no"), 0o644))
	c, err := newCannedLoom(static)
	require.NoError(t, err)
	c.now = func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) }
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return c, srv
}

func get(t *testing.T, url string) (int, string) {
	res, err := http.Get(url)
	require.NoError(t, err)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(b)
}

func TestCanned_ServesTheShellWithTheBuildTheSnapshotNames(t *testing.T) {
	c, srv := cannedForTest(t)
	code, body := get(t, srv.URL+"/")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `content="`+cannedBuild+`"`)

	snap, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var s struct{ Build string }
	require.NoError(t, json.Unmarshal(snap, &s))
	assert.Equal(t, cannedBuild, s.Build, "a build that differs makes the app reload for an upgrade")

	code, _ = get(t, srv.URL+"/secret.json")
	assert.Equal(t, http.StatusNotFound, code, "only the app's kinds of file")
}

func TestAppDir_PicksTheTreeTheAppNames(t *testing.T) {
	static := filepath.Join("internal", "web", "static")
	for _, c := range []struct{ app, static, want string }{
		{"", "", static},
		{"classic", "", static},
		{"next", "", filepath.Join("internal", "web", "dist")},
		{"next", "/x", "/x"},
		{"classic", "/x", "/x"},
	} {
		got, err := appDir(c.app, c.static)
		require.NoError(t, err, "%q %q", c.app, c.static)
		assert.Equal(t, c.want, got, "%q %q", c.app, c.static)
	}
	_, err := appDir("nope", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no app "nope"`)
}

func TestCanned_ServesTheNewAppsKindsOfFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html":      `<meta name="loom-build" content="__LOOM_BUILD__">`,
		"logo.svg":        "<svg/>",
		"font.woff2":      "wOF2",
		"THIRD_PARTY.txt": "licences",
		"notes.json":      "{}",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	c, err := newCannedLoom(dir)
	require.NoError(t, err)
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	for path, ctype := range map[string]string{
		"/logo.svg":        "image/svg+xml",
		"/font.woff2":      "font/woff2",
		"/THIRD_PARTY.txt": "text/plain; charset=utf-8",
	} {
		res, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode, path)
		assert.Equal(t, ctype, res.Header.Get("Content-Type"), path)
	}
	code, _ := get(t, srv.URL+"/notes.json")
	assert.Equal(t, http.StatusNotFound, code, "an extension outside the map is a 404")
}

func TestCanned_FilesCarryTheLayersCSP(t *testing.T) {
	_, srv := cannedForTest(t)
	res, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, contentSecurityPolicy, res.Header.Get("Content-Security-Policy"))
	assert.Contains(t, res.Header.Get("Content-Security-Policy"), "default-src 'self'")
	assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", res.Header.Get("Referrer-Policy"))

	res, err = http.Get(srv.URL + "/api/v1/sessions/7/mockup")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, mockupCSP, res.Header.Get("Content-Security-Policy"), "the file headers do not touch the mockup")
}

func TestCanned_AnswersTheRoutesTheScreensReadAndNamesTheRest(t *testing.T) {
	c, srv := cannedForTest(t)
	for _, p := range []string{"/api/v1/sessions/1/turns", "/api/v1/sessions/2/diff", "/api/v1/launch-defaults?workspace=loom"} {
		code, body := get(t, srv.URL+p)
		assert.Equal(t, http.StatusOK, code, p)
		assert.NotContains(t, body, `"now`, "%s: every relative time is filled", p)
	}
	code, body := get(t, srv.URL+"/api/v1/sessions/7/mockup")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "window.brainstorm", "the canned page is the companion's")
	assert.Empty(t, c.takeMisses())
	code, _ = get(t, srv.URL+"/api/v1/push-key")
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, []string{"GET /api/v1/push-key"}, c.takeMisses())
	code, _ = get(t, srv.URL+"/api/v1/sessions/1/message")
	assert.Equal(t, http.StatusNotFound, code, "no screen draws the last message, so the canned loom has no answer for it")
	assert.Equal(t, []string{"GET /api/v1/sessions/1/message"}, c.takeMisses())
}

func TestCanned_TheStreamStartsWithTheSnapshot(t *testing.T) {
	_, srv := cannedForTest(t)
	res, err := http.Get(srv.URL + "/api/v1/stream")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	r := bufio.NewReader(res.Body)
	ev, err := r.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: snapshot\n", ev)
	data, err := r.ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(data, "data: {"))
	var snap map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(data, "data: ")), &snap), "one line of JSON")
}

func TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("turns.json")
	require.NoError(t, err)
	var page struct {
		Turns []struct {
			Items []struct {
				Kind  string `json:"kind"`
				Text  string `json:"text"`
				Steps []struct {
					Agent    string `json:"agent"`
					Finished bool   `json:"finished"`
				} `json:"steps"`
			} `json:"items"`
		} `json:"turns"`
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	finished := map[string]bool{}
	thinking := ""
	for _, turn := range page.Turns {
		for _, it := range turn.Items {
			if it.Kind == "thinking" {
				thinking = it.Text
			}
			for _, st := range it.Steps {
				if st.Agent != "" {
					finished[st.Agent] = st.Finished
				}
			}
		}
	}
	require.Contains(t, finished, "a1f00dcafe0001", "the subagent the agent screen shows")
	assert.False(t, finished["a1f00dcafe0001"], "it is still running: its link keeps the ◐")
	require.Contains(t, finished, "a1f00dcafe0002")
	assert.True(t, finished["a1f00dcafe0002"], "a finished one draws no ◐")
	assert.NotEmpty(t, thinking, "a thinking item with text")
}

func TestFixture_SessionOnesAgentsCarryTheIdsTheTurnsLinkTo(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID     int `json:"id"`
			Agents []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"agents"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	for _, s := range snap.Sessions["1"] {
		if s.ID != 1 {
			continue
		}
		require.Len(t, s.Agents, 2)
		assert.Equal(t, "a1f00dcafe0001", s.Agents[0].ID, "recon, the running one")
		assert.Equal(t, "a1f00dcafe0002", s.Agents[1].ID, "verifier, the idle one")
		return
	}
	t.Fatal("no session 1")
}

func TestFixture_SessionTwoHoldsAPermissionAndAQuestionAsk(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID     int           `json:"id"`
			Agents []interface{} `json:"agents"`
			Asks   []struct {
				ID    string `json:"id"`
				Kind  string `json:"kind"`
				Agent string `json:"agent"`
				Tool  string `json:"tool"`
				Input []struct {
					Label string `json:"label"`
					Mono  bool   `json:"mono"`
				} `json:"input"`
				Suggestions []struct {
					Persists bool `json:"persists"`
				} `json:"suggestions"`
				Questions []struct {
					Question string `json:"question"`
					Multi    bool   `json:"multi"`
					Options  []struct {
						Label   string `json:"label"`
						Preview string `json:"preview"`
					} `json:"options"`
				} `json:"questions"`
			} `json:"asks"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	found := false
	for _, list := range snap.Sessions {
		for _, s := range list {
			if s.ID != 2 {
				assert.Empty(t, s.Asks, "only session 2 holds asks")
				continue
			}
			found = true
			assert.Empty(t, s.Agents)
			require.Len(t, s.Asks, 2)
			perm, q := s.Asks[0], s.Asks[1]
			assert.Equal(t, "fixture-permission", perm.ID)
			assert.Equal(t, "permission", perm.Kind)
			assert.Equal(t, "Bash", perm.Tool)
			require.Len(t, perm.Input, 2)
			assert.Equal(t, "command", perm.Input[0].Label)
			assert.True(t, perm.Input[0].Mono)
			assert.Equal(t, "description", perm.Input[1].Label)
			assert.False(t, perm.Input[1].Mono)
			require.Len(t, perm.Suggestions, 2)
			assert.True(t, perm.Suggestions[0].Persists)
			assert.False(t, perm.Suggestions[1].Persists)
			assert.Equal(t, "fixture-question", q.ID)
			assert.Equal(t, "question", q.Kind)
			assert.Equal(t, "recon", q.Agent)
			require.Len(t, q.Questions, 2)
			assert.Equal(t, "Which tests should run?", q.Questions[0].Question)
			assert.False(t, q.Questions[0].Multi)
			assert.Equal(t, "Which checks to add?", q.Questions[1].Question)
			assert.True(t, q.Questions[1].Multi)
			for qi, qq := range q.Questions {
				for _, o := range qq.Options {
					assert.Equal(t, qi == 0 && o.Label == "Pane only", o.Preview != "", "a preview on Pane only and no other option: %s", o.Label)
				}
			}
		}
	}
	require.True(t, found, "no session 2")
}

func TestFixture_SessionOneHoldsAWorktreeATmuxSessionAndAQueuedJob(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID       int    `json:"id"`
			Worktree string `json:"worktree"`
			Tmux     string `json:"tmux"`
			Jobs     []struct {
				ID      int    `json:"id"`
				State   string `json:"state"`
				Waiting string `json:"waiting"`
			} `json:"jobs"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	found := false
	for _, list := range snap.Sessions {
		for _, s := range list {
			if s.ID != 1 {
				assert.Empty(t, s.Worktree, "no other session has a worktree")
				assert.Empty(t, s.Tmux, "no other session has a tmux session")
				assert.Empty(t, s.Jobs, "no other session has a job")
				continue
			}
			found = true
			assert.Equal(t, "/work/loom/fix-pane-cursor", s.Worktree)
			assert.Equal(t, "loom_fix-pane-cursor", s.Tmux)
			states := map[int]string{}
			for _, j := range s.Jobs {
				states[j.ID] = j.State
				if j.ID == 216 {
					assert.Equal(t, "waiting for 6 free cores", j.Waiting)
				}
			}
			assert.Equal(t, map[int]string{214: "running", 215: "held", 216: "queued", 212: "done", 209: "failed"}, states)
		}
	}
	require.True(t, found, "no session 1")
}

func TestFixture_SessionSixHoldsASecondOlderFlowSoThePickerShows(t *testing.T) {
	c := &cannedLoom{now: func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) }}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID    int `json:"id"`
			Flows []struct {
				Plan    string `json:"plan"`
				Stage   string `json:"stage"`
				Status  string `json:"status"`
				Updated string `json:"updated"`
			} `json:"flows"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	found := false
	for _, list := range snap.Sessions {
		for _, s := range list {
			if s.ID != 6 {
				assert.Empty(t, s.Flows, "no other session has a flow")
				continue
			}
			found = true
			require.Len(t, s.Flows, 2)
			first, second := s.Flows[0], s.Flows[1]
			assert.Equal(t, "docs/superpowers/plans/2026-11-09-flow-visualisation.md", first.Plan)
			assert.Equal(t, "parked", first.Status)
			assert.Equal(t, "spec", second.Stage)
			assert.Equal(t, "designed", second.Status)
			assert.NotEqual(t, first.Plan, second.Plan)
			t1, err := time.Parse(time.RFC3339, first.Updated)
			require.NoError(t, err)
			t2, err := time.Parse(time.RFC3339, second.Updated)
			require.NoError(t, err)
			assert.True(t, t2.Before(t1), "the second flow is older")
		}
	}
	require.True(t, found, "no session 6")
}

func TestFixture_TheDeviceIsExpiredAndPausedAndTwoNoticesOneAnError(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	c := &cannedLoom{now: func() time.Time { return now }}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Device struct {
			Notify struct {
				PausedUntil string `json:"paused_until"`
			} `json:"notify"`
			Push string `json:"push"`
		} `json:"device"`
		Notices []struct {
			Text  string `json:"text"`
			Error bool   `json:"error"`
			At    string `json:"at"`
		} `json:"notices"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	assert.Equal(t, "expired", snap.Device.Push)
	paused, err := time.Parse(time.RFC3339, snap.Device.Notify.PausedUntil)
	require.NoError(t, err)
	assert.Equal(t, now.Add(40*time.Minute), paused)
	require.Len(t, snap.Notices, 2)
	assert.False(t, snap.Notices[0].Error)
	assert.True(t, snap.Notices[1].Error)
	for _, n := range snap.Notices {
		assert.NotEmpty(t, n.Text)
		at, err := time.Parse(time.RFC3339, n.At)
		require.NoError(t, err)
		assert.True(t, at.Before(now), "a notice's age is relative")
	}
}

func TestFixture_TheBoardHoldsAFinalRowARunningStepAnOtherRunAndAnUnpricedAgent(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("flow.json")
	require.NoError(t, err)
	type step struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Status  string `json:"status"`
		Ended   string `json:"ended"`
		Waiting string `json:"waiting"`
	}
	type row struct {
		ID     string            `json:"id"`
		Status string            `json:"status"`
		Cells  map[string]string `json:"cells"`
		Steps  []step            `json:"steps"`
	}
	var board struct {
		Stories []row    `json:"stories"`
		Final   *row     `json:"final"`
		Kept    []string `json:"kept"`
		Others  []struct {
			ID    string `json:"id"`
			Start string `json:"start"`
		} `json:"others"`
		Costs struct {
			Unpriced []string `json:"unpriced"`
		} `json:"costs"`
	}
	require.NoError(t, json.Unmarshal(raw, &board))

	require.NotNil(t, board.Final, "the final row")
	assert.Equal(t, "F", board.Final.ID)
	assert.Equal(t, map[string]string{"gates": "passed", "review": "running", "fix": "todo"}, board.Final.Cells)
	require.Len(t, board.Final.Steps, 2)
	assert.Equal(t, []string{"gates", "review"}, []string{board.Final.Steps[0].Kind, board.Final.Steps[1].Kind})
	assert.Equal(t, []string{"passed", "running"}, []string{board.Final.Steps[0].Status, board.Final.Steps[1].Status})

	var ids []string
	for _, s := range board.Stories {
		ids = append(ids, s.ID)
	}
	assert.Equal(t, []string{"1", "2", "3", "4", "5"}, ids)
	four := board.Stories[3]
	assert.Equal(t, "developing", four.Status)
	assert.Equal(t, "running", four.Cells["dev"])
	require.Len(t, four.Steps, 1)
	assert.Equal(t, "dev", four.Steps[0].Kind)
	assert.Equal(t, "running", four.Steps[0].Status)
	assert.Empty(t, four.Steps[0].Ended, "the step is still running")
	assert.NotEmpty(t, four.Steps[0].Waiting)
	assert.Equal(t, "asks", board.Stories[2].Cells["dev"])

	require.Len(t, board.Others, 1)
	assert.NotEmpty(t, board.Others[0].ID)
	assert.NotEmpty(t, board.Others[0].Start)
	assert.Equal(t, []string{four.Steps[0].Name}, board.Costs.Unpriced)
	assert.NotEmpty(t, board.Kept)
}

func TestFixture_TheTurnsHoldEveryItemKindAnInterruptedStepAndTheTreesNodes(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("turns.json")
	require.NoError(t, err)
	type node struct {
		T string  `json:"t"`
		C []*node `json:"c"`
	}
	var page struct {
		Turns []struct {
			Items []struct {
				Kind   string  `json:"kind"`
				Error  bool    `json:"error"`
				Blocks []*node `json:"blocks"`
				Steps  []struct {
					State string `json:"state"`
				} `json:"steps"`
			} `json:"items"`
		} `json:"turns"`
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	kinds := map[string]bool{}
	nodes := map[string]bool{}
	var walk func(ns []*node)
	walk = func(ns []*node) {
		for _, n := range ns {
			nodes[n.T] = true
			walk(n.C)
		}
	}
	notes, errNotes, interrupted := 0, 0, false
	for _, turn := range page.Turns {
		for _, it := range turn.Items {
			kinds[it.Kind] = true
			if it.Kind == "note" {
				notes++
				if it.Error {
					errNotes++
				}
			}
			for _, st := range it.Steps {
				interrupted = interrupted || st.State == "interrupted"
			}
			walk(it.Blocks)
		}
	}
	for _, k := range []string{"text", "thinking", "steps", "compacted", "note"} {
		assert.True(t, kinds[k], "item kind %s", k)
	}
	assert.Equal(t, 2, notes, "two notes")
	assert.Equal(t, 1, errNotes, "one of them an error")
	assert.True(t, interrupted, "an interrupted step")
	for _, k := range []string{"h", "p", "ul", "ol", "li", "quote", "code", "em", "strong", "a", "hr", "table", "tr", "th", "td", "text", "code_inline"} {
		assert.True(t, nodes[k], "node kind %s", k)
	}
}

func TestFixture_TheDiffHoldsADeletedAndAnOmittedFile(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("diff.json")
	require.NoError(t, err)
	var patch struct {
		Files []struct {
			Path    string `json:"path"`
			Status  string `json:"status"`
			Omitted string `json:"omitted"`
			Hunks   []any  `json:"hunks"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(raw, &patch))
	require.GreaterOrEqual(t, len(patch.Files), 5)
	for i, p := range []string{"ui/pane.go", "ui/header.go", "assets/cursor.png"} {
		assert.Equal(t, p, patch.Files[i].Path)
	}
	deleted, omitted := false, false
	for _, f := range patch.Files {
		deleted = deleted || f.Status == "D"
		omitted = omitted || (f.Omitted == "lines" && len(f.Hunks) == 0)
	}
	assert.True(t, deleted, "a deleted file")
	assert.True(t, omitted, "a file omitted for its length, with no hunks")
}

func TestPickScreens(t *testing.T) {
	all, err := pickScreens("")
	require.NoError(t, err)
	assert.Equal(t, screens, all)
	two, err := pickScreens("conversation, fleet")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"conversation", "#s/1"}, {"fleet", ""}}, two)
	for _, retired := range []string{"session", "turns"} {
		_, err = pickScreens(retired)
		assert.Error(t, err, "%s: a session opens on its conversation, and its old screens are gone", retired)
	}
	_, err = pickScreens("fleet,nope")
	assert.Error(t, err)
}

func TestScreens_PairIsShotUnpairedAndAddToHomeAsABrowserTab(t *testing.T) {
	two, err := pickScreens("pair,add-to-home")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"pair", "#pair"}, {"add-to-home", ""}}, two)
	assert.Equal(t, screenMode{unpaired: true}, modeOf(two[0]))
	assert.Equal(t, screenMode{browserTab: true}, modeOf(two[1]))
	seen := map[string]bool{}
	for _, sc := range screens {
		assert.False(t, seen[sc.name], "the screen name %s is repeated", sc.name)
		seen[sc.name] = true
		if sc.name != "pair" && sc.name != "add-to-home" {
			assert.Equal(t, screenMode{}, modeOf(sc), "%s has a mode", sc.name)
		}
	}
}

func TestCanned_AnUnpairedLoomAnswersEveryAPIRequest401AsAuthedDoes(t *testing.T) {
	c, srv := cannedForTest(t)
	code, _ := get(t, srv.URL+"/api/v1/sessions/1/turns")
	assert.Equal(t, http.StatusOK, code, "the paired loom is as it was")

	c.unpaired = true
	// -serve mounts apiHandler and everything else ServeHTTP, each with a branch of its own.
	apiOnly := httptest.NewServer(c.apiHandler())
	t.Cleanup(apiOnly.Close)
	for _, rq := range []struct{ method, base, path string }{
		{http.MethodGet, srv.URL, "/api/v1/stream"},
		{http.MethodGet, srv.URL, "/api/v1/sessions/1/turns"},
		{http.MethodGet, srv.URL, "/api/v1/push-key"},
		{http.MethodPost, srv.URL, "/api/v1/pair"},
		{http.MethodGet, apiOnly.URL, "/api/v1/stream"},
		{http.MethodGet, apiOnly.URL, "/api/v1/sessions/1/turns"},
		{http.MethodPost, apiOnly.URL, "/api/v1/pair"},
	} {
		var res *http.Response
		var err error
		if rq.method == http.MethodPost {
			res, err = http.Post(rq.base+rq.path, "application/json", strings.NewReader("{}"))
		} else {
			res, err = http.Get(rq.base + rq.path)
		}
		require.NoError(t, err, rq.path)
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, res.StatusCode, rq.path)
		assert.Equal(t, "application/json", res.Header.Get("Content-Type"), rq.path)
		var got map[string]string
		require.NoError(t, json.Unmarshal(body, &got), rq.path)
		assert.Equal(t, map[string]string{"error": "this phone is not paired"}, got, rq.path)
	}
	assert.Empty(t, c.takeMisses(), "a refusal is not a miss")

	code, body := get(t, srv.URL+"/")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `content="`+cannedBuild+`"`)
}

func TestEmulation_ABrowserTabIsNotStandalone(t *testing.T) {
	d := devices[defaultDevice]
	app, tab := emulation(d, true), emulation(d, false)
	require.Equal(t, len(app), len(tab))
	var appSrc, tabSrc string
	for i := range app {
		require.Equal(t, app[i].method, tab[i].method)
		switch app[i].method {
		case "Page.addScriptToEvaluateOnNewDocument":
			appSrc = app[i].params.(map[string]any)["source"].(string)
			tabSrc = tab[i].params.(map[string]any)["source"].(string)
		case "Emulation.setUserAgentOverride":
			assert.Equal(t, d.userAgent, app[i].params.(map[string]any)["userAgent"])
			assert.Equal(t, d.userAgent, tab[i].params.(map[string]any)["userAgent"])
		}
	}
	assert.Contains(t, appSrc, "get: () => true")
	assert.NotContains(t, appSrc, "get: () => false")
	assert.Contains(t, tabSrc, "get: () => false")
	assert.NotContains(t, tabSrc, "get: () => true")
}

func TestEmulation_ADesktopIsNotMobileAndHasNoTouchNoInsets(t *testing.T) {
	for _, name := range []string{"desktop", "desktop-narrow"} {
		d := devices[name]
		for _, standalone := range []bool{true, false} {
			methods := map[string]map[string]any{}
			for _, s := range emulation(d, standalone) {
				m, _ := s.params.(map[string]any)
				methods[s.method] = m
			}
			assert.NotContains(t, methods, "Emulation.setSafeAreaInsetsOverride", name)
			assert.NotContains(t, methods, "Emulation.setTouchEmulationEnabled", name)
			metrics := methods["Emulation.setDeviceMetricsOverride"]
			require.NotNil(t, metrics, name)
			assert.Equal(t, false, metrics["mobile"], name)
			assert.Equal(t, d.width, metrics["width"], name)
			assert.Equal(t, d.height, metrics["height"], name)
			assert.Equal(t, 1.0, metrics["deviceScaleFactor"], name)
			assert.NotContains(t, metrics, "screenOrientation", name)
			ua := methods["Emulation.setUserAgentOverride"]
			require.NotNil(t, ua, name)
			assert.Equal(t, d.userAgent, ua["userAgent"], name)
			assert.NotEqual(t, "iPhone", ua["platform"], name)
		}
	}
	phone := map[string]map[string]any{}
	for _, s := range emulation(devices["iphone15"], true) {
		m, _ := s.params.(map[string]any)
		phone[s.method] = m
	}
	assert.Contains(t, phone, "Emulation.setSafeAreaInsetsOverride")
	assert.Contains(t, phone, "Emulation.setTouchEmulationEnabled")
	assert.Equal(t, true, phone["Emulation.setDeviceMetricsOverride"]["mobile"])
}

func TestForDevice_DropsOnlyAddToHomeOnADesktop(t *testing.T) {
	var want []screen
	for _, sc := range screens {
		if sc.name != "add-to-home" {
			want = append(want, sc)
		}
	}
	for _, name := range []string{"desktop", "desktop-narrow"} {
		got := forDevice(devices[name], screens)
		assert.Equal(t, want, got, name)
		assert.Len(t, got, len(screens)-1, name)
		assert.Contains(t, got, screen{"pair", "#pair"}, name)
	}
	assert.Contains(t, screens, screen{"add-to-home", ""}, "screens is not changed")
	assert.Equal(t, screens, forDevice(devices[defaultDevice], screens))
	assert.Empty(t, forDevice(devices["desktop"], []screen{{"add-to-home", ""}}))
}

func TestRun_AnUnknownDeviceNamesTheOnesItHas(t *testing.T) {
	err := run([]string{"-out", t.TempDir(), "-device", "ipad"})
	require.Error(t, err)
	for _, s := range []string{"ipad", "desktop", "desktop-narrow", "iphone15", "iphone16"} {
		assert.Contains(t, err.Error(), s)
	}
}

func TestRun_AddToHomeAloneOnADesktopRendersNothing(t *testing.T) {
	err := run([]string{"-out", t.TempDir(), "-device", "desktop", "-screens", "add-to-home", "-browser", "no-such-browser-binary"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no screen to render on desktop")
	assert.NotContains(t, err.Error(), "no-such-browser-binary", "the check runs before a browser is looked for")
}

// TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser runs only with
// LOOM_WEBSHOT_BROWSER set, as TestRun_RendersEveryScreenInARealBrowser does.
func TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser(t *testing.T) {
	which := os.Getenv("LOOM_WEBSHOT_BROWSER")
	if which == "" {
		t.Skip("set LOOM_WEBSHOT_BROWSER to run it")
	}
	for _, name := range []string{"desktop", "desktop-narrow"} {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			args := []string{"-app", "next", "-static", filepath.Join("..", "..", "internal", "web", "dist"), "-device", name, "-out", out, "-settle", "800ms"}
			if which != "1" {
				_, err := exec.LookPath(which)
				require.NoError(t, err)
				args = append(args, "-browser", which)
			}
			require.NoError(t, run(args), "every screen draws with no console error, CSP violation or route the fixture lacks")
			f, err := os.Open(filepath.Join(out, "fleet.png"))
			require.NoError(t, err)
			defer f.Close()
			img, err := png.Decode(f)
			require.NoError(t, err)
			d := devices[name]
			assert.Equal(t, [2]int{d.width, d.height}, [2]int{img.Bounds().Dx(), img.Bounds().Dy()}, "a bare viewport at scale 1")
			_, err = os.Stat(filepath.Join(out, "add-to-home.png"))
			assert.True(t, os.IsNotExist(err), "add-to-home is phone only")
		})
	}
}

// TestRun_RendersEveryScreenInARealBrowser drives Chromium end to end. It
// runs only with LOOM_WEBSHOT_BROWSER set (to a binary, or "1" for the one on
// PATH), since CI has no browser.
func TestRun_RendersEveryScreenInARealBrowser(t *testing.T) {
	which := os.Getenv("LOOM_WEBSHOT_BROWSER")
	if which == "" {
		t.Skip("set LOOM_WEBSHOT_BROWSER to run it")
	}
	args := []string{"-out", t.TempDir(), "-static", filepath.Join("..", "..", "internal", "web", "static"), "-settle", "800ms"}
	if which != "1" {
		_, err := exec.LookPath(which)
		require.NoError(t, err)
		args = append(args, "-browser", which)
	}
	require.NoError(t, run(args), "every screen draws with no console error and no route the fixture lacks")
}

// serveForTest runs serveAPI on a free port and returns its address, the
// misses it reports, a cancel and the channel its result arrives on.
func serveForTest(t *testing.T) (string, chan string, context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addrs := make(chan string, 1)
	misses := make(chan string, 8)
	done := make(chan error, 1)
	go func() {
		done <- serveAPI(ctx, "127.0.0.1:0", func(a net.Addr) { addrs <- a.String() }, func(r string) { misses <- r })
	}()
	select {
	case a := <-addrs:
		return a, misses, cancel, done
	case err := <-done:
		t.Fatalf("serveAPI returned before it listened: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("serveAPI never announced its address")
	}
	return "", nil, nil, nil
}

func TestServeAPI_AnswersTheCannedRoutesAndNothingElse(t *testing.T) {
	// A page in the working directory, so a fall-through to a file server
	// would answer 200 for it.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644))
	t.Chdir(dir)
	addr, misses, _, _ := serveForTest(t)
	base := "http://" + addr

	code, body := get(t, base+"/api/v1/sessions/1/turns")
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, `"now`)

	code, _ = get(t, base+"/index.html")
	assert.Equal(t, http.StatusNotFound, code, "no file is served")

	code, _ = get(t, base+"/api/v1/push-key")
	assert.Equal(t, http.StatusNotFound, code)
	select {
	case m := <-misses:
		assert.Equal(t, "GET /api/v1/push-key", m)
	case <-time.After(2 * time.Second):
		t.Fatal("onMiss was not called")
	}

	res, err := http.Get(base + "/api/v1/stream")
	require.NoError(t, err)
	defer res.Body.Close()
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: snapshot\n", line)
}

func TestServeAPI_StopsWhenItsContextEndsEvenWithAStreamOpen(t *testing.T) {
	addr, _, cancel, done := serveForTest(t)
	res, err := http.Get("http://" + addr + "/api/v1/stream")
	require.NoError(t, err)
	defer res.Body.Close()
	_, err = bufio.NewReader(res.Body).ReadString('\n')
	require.NoError(t, err)

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("serveAPI did not stop with a stream open")
	}
}

func TestServeAPI_AnAddressInUseIsAnError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	err = serveAPI(context.Background(), ln.Addr().String(), func(net.Addr) { t.Error("announced an address it did not listen on") }, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "serve 127.0.0.1:")
}

func TestRun_ServeRefusesWhatItWouldRender(t *testing.T) {
	for _, args := range [][]string{
		{"-serve", "127.0.0.1:0", "-out", t.TempDir()},
		{"-serve", "127.0.0.1:0", "-screens", "fleet"},
	} {
		done := make(chan error, 1)
		go func() { done <- run(args) }()
		select {
		case err := <-done:
			require.Error(t, err, "%v", args)
			assert.Contains(t, err.Error(), "-serve renders nothing", "%v", args)
		case <-time.After(2 * time.Second):
			t.Fatalf("%v: run served instead of refusing", args)
		}
	}
}

func TestPickScreens_TheAgentScreenIsSessionOnesRunningSubagent(t *testing.T) {
	got, err := pickScreens("agent")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"agent", "#s/1/agent/a1f00dcafe0001"}}, got)
}

func TestCanned_ASubagentsTurnsAreTheAgentFixture(t *testing.T) {
	c, srv := cannedForTest(t)
	_, plain := get(t, srv.URL+"/api/v1/sessions/1/turns")
	var conversation map[string]any
	require.NoError(t, json.Unmarshal([]byte(plain), &conversation))
	assert.NotContains(t, conversation, "agent", "the conversation's page names no subagent")

	code, body := get(t, srv.URL+"/api/v1/sessions/1/turns?agent=a1f00dcafe0001")
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, `"now`, "every relative time is filled")
	var page struct {
		Agent struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Missing bool   `json:"missing"`
		} `json:"agent"`
		Turns []struct {
			From string `json:"from"`
			Text string `json:"text"`
		} `json:"turns"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &page))
	assert.Equal(t, "a1f00dcafe0001", page.Agent.ID)
	assert.Equal(t, "running", page.Agent.State)
	assert.False(t, page.Agent.Missing)
	require.NotEmpty(t, page.Turns)
	assert.Equal(t, "brief", page.Turns[0].From, "a subagent's page opens with its brief")
	assert.Greater(t, strings.Count(page.Turns[0].Text, "\n"), 12, "long enough to fold after 12 lines")
	assert.Empty(t, c.takeMisses())
}
