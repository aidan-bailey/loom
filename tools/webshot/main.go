// Command webshot renders the phone app's screens as they look on a phone:
// the owner's iPhone 15 by default (-device iphone16 is the same screen), at
// its viewport, pixel ratio and safe-area insets, as a Home Screen app,
// framed with its status bar, Dynamic Island and home indicator. It serves internal/web/static from disk against a
// canned loom (tools/webshot/fixture), drives headless Chromium over
// --remote-debugging-pipe, and writes one PNG per screen.
//
//	go run ./tools/webshot -out /tmp/shots
//	go run ./tools/webshot -out /tmp/shots -screens fleet,conversation -guides
//
// It never touches a daemon, a session or the user's tmux server. Console
// errors, uncaught exceptions and API routes the fixture can't answer are
// printed after each screen, and any of them makes the exit status 1.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// screen is one route of the app to render.
type screen struct{ name, hash string }

// screens are the routes webshot knows, in the order it renders them. The
// session screens are of session 1 (working, with a PR, agents and a diff),
// session 2 (waiting on a permission: its conversation ends in the needs-you
// box) and session 7 (your turn, with a fresh brainstorm mockup). A session
// opens on its conversation; "info" is the tab that holds its tiles, agents
// and actions.
var screens = []screen{
	{"fleet", ""},
	{"conversation", "#s/1"},
	{"waiting", "#s/2"},
	{"info", "#s/1/info"},
	{"diff", "#s/1/diff"},
	{"mockup", "#s/7/mockup"},
	{"mockup-full", "#s/7/mockup/full"},
	{"new", "#new"},
	{"settings", "#settings"},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "webshot:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("webshot", flag.ContinueOnError)
	out := fs.String("out", "", "directory to write the PNGs to (required; keep it out of the worktree)")
	only := fs.String("screens", "", "comma-separated screens to render (default all: "+screenNames()+")")
	dev := fs.String("device", defaultDevice, "the device to render as: iphone15 or iphone16")
	static := fs.String("static", filepath.Join("internal", "web", "static"), "the app's files")
	browserBin := fs.String("browser", "", "Chromium or Chrome to run (default: the first of chromium, chromium-browser, google-chrome, google-chrome-stable on PATH)")
	bare := fs.Bool("bare", false, "write the viewport only: no bezel, status bar, island or home indicator")
	guides := fs.Bool("guides", false, "tint the unsafe areas (the safe-area insets)")
	settle := fs.Duration("settle", 1200*time.Millisecond, "how long a screen gets to fetch and draw after it loads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	d, ok := devices[*dev]
	if !ok {
		return fmt.Errorf("no device %q", *dev)
	}
	todo, err := pickScreens(*only)
	if err != nil {
		return err
	}
	bin, err := findBrowser(*browserBin)
	if err != nil {
		return err
	}
	loom, err := newCannedLoom(*static)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: loom, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	origin := "http://" + ln.Addr().String() + "/"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := launchBrowser(ctx, bin)
	if err != nil {
		return err
	}
	defer b.close()
	pg, err := b.openPage(ctx, d)
	if err != nil {
		return err
	}

	var bar image.Image
	if !*bare {
		if bar, err = pg.statusBar(ctx, d); err != nil {
			return err
		}
	}

	failed := false
	for _, sc := range todo {
		file := filepath.Join(*out, sc.name+".png")
		problems, err := shoot(ctx, pg, loom, d, bar, sc, origin, *settle, file, frameOpts{bezel: !*bare, guides: *guides})
		if err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
		fmt.Printf("%s\t%s\n", sc.name, file)
		for _, p := range problems {
			fmt.Printf("  ! %s\n", p)
			failed = true
		}
	}
	if failed {
		return errors.New("a screen reported problems")
	}
	return nil
}

func screenNames() string {
	names := make([]string, len(screens))
	for i, s := range screens {
		names[i] = s.name
	}
	return strings.Join(names, ",")
}

// pickScreens is the screens named in only, a comma-separated list, in its
// order; all of them when only is empty.
func pickScreens(only string) ([]screen, error) {
	if only == "" {
		return screens, nil
	}
	var out []screen
	for _, name := range strings.Split(only, ",") {
		name = strings.TrimSpace(name)
		i := slices.IndexFunc(screens, func(s screen) bool { return s.name == name })
		if i < 0 {
			return nil, fmt.Errorf("no screen %q (have %s)", name, screenNames())
		}
		out = append(out, screens[i])
	}
	return out, nil
}

func findBrowser(given string) (string, error) {
	if given != "" {
		return exec.LookPath(given)
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chromium on PATH: pass -browser")
}

// browser is a headless Chromium on a pipe, with its own throwaway profile.
type browser struct {
	cmd     *exec.Cmd
	cdp     *cdpClient
	profile string

	mu       sync.Mutex
	problems []string // console errors and exceptions since the last take
}

func launchBrowser(ctx context.Context, bin string) (*browser, error) {
	profile, err := os.MkdirTemp("", "webshot-")
	if err != nil {
		return nil, err
	}
	// The browser reads calls from its fd 3 and writes replies to its fd 4.
	toR, toW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fromR, fromW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "--headless", "--remote-debugging-pipe", "--no-first-run", "--no-default-browser-check",
		"--disable-gpu", "--hide-scrollbars", "--mute-audio", "--user-data-dir="+profile, "about:blank")
	cmd.ExtraFiles = []*os.File{toR, fromW}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	_ = toR.Close()
	_ = fromW.Close()
	b := &browser{cmd: cmd, profile: profile}
	b.cdp = newCDPClient(fromR, toW, b.event)
	if err := b.cdp.call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

func (b *browser) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = b.cdp.call(ctx, "", "Browser.close", nil, nil)
	done := make(chan struct{})
	go func() { _ = b.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = b.cmd.Process.Kill()
		<-done
	}
	_ = os.RemoveAll(b.profile)
}

// event keeps the page's console errors and uncaught exceptions.
func (b *browser) event(method, _ string, params json.RawMessage) {
	if problem := pageProblem(method, params); problem != "" {
		b.mu.Lock()
		b.problems = append(b.problems, problem)
		b.mu.Unlock()
	}
}

// pageProblem words an event that reports something wrong on the page, or is
// "" for any other event. A failed API request is left to the canned loom,
// which names the route.
func pageProblem(method string, params json.RawMessage) string {
	switch method {
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(params, &p) == nil {
			return "uncaught: " + firstLine(p.ExceptionDetails.Exception.Description+" "+p.ExceptionDetails.Text)
		}
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		if json.Unmarshal(params, &p) == nil && p.Type == "error" {
			var parts []string
			for _, a := range p.Args {
				if a.Value != nil {
					parts = append(parts, fmt.Sprint(a.Value))
				} else {
					parts = append(parts, a.Description)
				}
			}
			return "console.error: " + firstLine(strings.Join(parts, " "))
		}
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		if json.Unmarshal(params, &p) == nil && p.Entry.Level == "error" && !strings.Contains(p.Entry.URL, "/api/") {
			return "browser: " + firstLine(p.Entry.Text+" "+p.Entry.URL)
		}
	}
	return ""
}

func (b *browser) takeProblems() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.problems
	b.problems = nil
	return p
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// page is a tab emulating the device.
type page struct {
	b       *browser
	session string
}

// openPage opens a tab and makes it the device's Home Screen app: its
// viewport, pixel ratio, touch, user agent and safe-area insets, with
// navigator.standalone set as iOS sets it (Chromium ignores an emulated
// display-mode), and with service workers bypassed so every load is the
// files on disk.
func (b *browser) openPage(ctx context.Context, d device) (*page, error) {
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := b.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &t); err != nil {
		return nil, err
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.cdp.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, &a); err != nil {
		return nil, err
	}
	p := &page{b: b, session: a.SessionID}
	for _, s := range emulation(d) {
		if err := b.cdp.call(ctx, p.session, s.method, s.params, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// cdpStep is one call that sets a tab up.
type cdpStep struct {
	method string
	params any
}

// emulation is the calls that make a tab the device's Home Screen app.
func emulation(d device) []cdpStep {
	return []cdpStep{
		{"Page.enable", nil},
		{"Runtime.enable", nil},
		{"Log.enable", nil},
		{"Network.enable", nil},
		{"Network.setBypassServiceWorker", map[string]any{"bypass": true}},
		{"Emulation.setDeviceMetricsOverride", map[string]any{
			"width": d.width, "height": d.height, "deviceScaleFactor": d.scale, "mobile": true,
			"screenWidth": d.width, "screenHeight": d.height,
			"screenOrientation": map[string]any{"type": "portraitPrimary", "angle": 0},
		}},
		{"Emulation.setSafeAreaInsetsOverride", map[string]any{"insets": map[string]any{
			"top": d.insetTop, "topMax": d.insetTop, "bottom": d.insetBottom, "bottomMax": d.insetBottom,
			"left": 0, "leftMax": 0, "right": 0, "rightMax": 0,
		}}},
		{"Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}},
		{"Emulation.setUserAgentOverride", map[string]any{"userAgent": d.userAgent, "platform": "iPhone"}},
		{"Emulation.setScrollbarsHidden", map[string]any{"hidden": true}},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{
			"source": `Object.defineProperty(Navigator.prototype, "standalone", {get: () => true, configurable: true});`,
		}},
	}
}

// query runs a JavaScript expression in the page and decodes its value.
func (p *page) query(ctx context.Context, expr string, out any) error {
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := p.b.cdp.call(ctx, p.session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return fmt.Errorf("run %q: %s", expr, r.ExceptionDetails.Text)
	}
	return json.Unmarshal(r.Result.Value, out)
}

// appDrawn is true once the page is the app and it has drawn into #app.
const appDrawn = `document.readyState === "complete" && location.protocol === "http:" && ` +
	`!!document.querySelector("#app") && document.querySelector("#app").childElementCount > 0`

// shoot loads one screen fresh, waits until the app has drawn and settled,
// captures the viewport and writes it framed to file, bar laid over it. It returns what went
// wrong on the page: console errors, exceptions and routes the canned loom
// could not answer.
func shoot(ctx context.Context, p *page, loom *cannedLoom, d device, bar image.Image, sc screen, origin string, settle time.Duration, file string, o frameOpts) ([]string, error) {
	if err := p.b.cdp.call(ctx, p.session, "Page.navigate", map[string]any{"url": "about:blank"}, nil); err != nil {
		return nil, err
	}
	p.b.takeProblems()
	loom.takeMisses()
	if err := p.b.cdp.call(ctx, p.session, "Page.navigate", map[string]any{"url": origin + sc.hash}, nil); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var ready bool
		if err := p.query(ctx, appDrawn, &ready); err == nil && ready {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the app drew nothing in 15s")
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(settle)

	img, err := p.capture(ctx, d)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, frame(img, bar, d, o)); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	problems := p.b.takeProblems()
	for _, m := range loom.takeMisses() {
		problems = append(problems, "the canned loom has no answer for "+m+": add it to tools/webshot/fixture")
	}
	return problems, nil
}
