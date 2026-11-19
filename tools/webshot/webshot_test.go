package main

import (
	"bufio"
	"context"
	"encoding/json"
	"image"
	"image/color"
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
		"a": "2026-11-03T10:00:00Z", "b": []any{"2026-11-03T09:58:00Z", "2026-11-03T11:40:00Z"}, "c": "nowhere", "d": 3.0,
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
