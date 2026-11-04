package main

import (
	"bufio"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"math"
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
	require.NoError(t, os.WriteFile(filepath.Join(static, "secret.txt"), []byte("no"), 0o644))
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

	code, _ = get(t, srv.URL+"/secret.txt")
	assert.Equal(t, http.StatusNotFound, code, "only the app's kinds of file")
}

func TestCanned_AnswersTheRoutesTheScreensReadAndNamesTheRest(t *testing.T) {
	c, srv := cannedForTest(t)
	for _, p := range []string{"/api/v1/sessions/1/message", "/api/v1/sessions/1/turns", "/api/v1/sessions/2/diff", "/api/v1/launch-defaults?workspace=loom"} {
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

func TestFixture_TheLastMessageIsTheTreesText(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID          int    `json:"id"`
			LastMessage string `json:"last_message"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	raw, err = c.fixture("message.json")
	require.NoError(t, err)
	var msg struct{ Text string }
	require.NoError(t, json.Unmarshal(raw, &msg))
	for _, s := range snap.Sessions["1"] {
		if s.ID == 1 {
			assert.Equal(t, msg.Text, s.LastMessage, "the app draws the tree only when its text is the session's last message")
			return
		}
	}
	t.Fatal("no session 1")
}

func TestPickScreens(t *testing.T) {
	all, err := pickScreens("")
	require.NoError(t, err)
	assert.Equal(t, screens, all)
	two, err := pickScreens("turns, fleet")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"turns", "#s/1/turns"}, {"fleet", ""}}, two)
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
