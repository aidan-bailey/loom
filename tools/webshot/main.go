// Command webshot renders the phone app's screens as they look on a phone:
// the owner's iPhone 15 by default (-device iphone16 is the same screen), at
// its viewport, pixel ratio and safe-area insets, as a Home Screen app,
// framed with its status bar, Dynamic Island and home indicator. It serves internal/web/static from disk against a
// canned loom (tools/webshot/fixture), drives headless Chromium over
// --remote-debugging-pipe, and writes one PNG per screen.
//
//	go run ./tools/webshot -out /tmp/shots
//	go run ./tools/webshot -out /tmp/shots -screens fleet,conversation -guides
//	go run ./tools/webshot -app next -out /tmp/shots
//
// -app picks the files to serve: classic (internal/web/static, the default)
// or next (internal/web/dist); -static overrides the directory. Every file
// goes out under the web layer's Content-Security-Policy.
//
//	go run ./tools/webshot -serve 127.0.0.1:5670
//
// -serve answers the canned /api/v1 alone on that address until killed, for
// the new app's dev server to proxy to, and renders nothing.
//
//	go run ./tools/webshot -app next -device desktop -out /tmp/shots
//	go run ./tools/webshot -app next -device desktop-narrow -out /tmp/shots
//
// -device desktop and desktop-narrow are bare browser windows at 1440×900 and
// 1100×800: scale 1, no touch, no insets, nothing drawn over the screenshot.
// They skip add-to-home, which only an iPhone browser tab shows.
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
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// screen is one route of the app to render.
type screen struct{ name, hash string }

// screens are the routes webshot knows, in the order it renders them. The
// session screens are of session 1 (working, with a PR, agents and a diff),
// session 2 (waiting on a permission: its conversation ends in the needs-you
// box) and session 7 (your turn, with a fresh brainstorm mockup). A session
// opens on its conversation; "info" is the tab that holds its tiles, agents
// and actions; "agent" is the screen of session 1's running subagent (recon,
// the one its conversation links to); "job" is session 1's running job.
// The flow screens are session 6's flow (a run parked on story 3): its
// board, story 1's row, its spec, the host's Flows screen and the flow
// board (every flow by its stage). "pair" is shot
// with every /api/ request answered 401 (an unpaired device), "add-to-home"
// as an iPhone browser tab (not standalone). "palette" is session 1's route
// with the command palette opened by its chord, shot on a desktop under -app
// next only. "terminal" is session 1's worktree shell, drawn from the canned
// loom's terminal route, and "terminal-select" the same with the phone's copy
// sheet open (Select clicked once the terminal is live); both are shot under
// -app next only, the second on a phone only.
var screens = []screen{
	{"fleet", ""},
	{"conversation", "#s/1"},
	{"waiting", "#s/2"},
	{"info", "#s/1/info"},
	{"terminal", "#s/1/terminal"},
	{"terminal-select", "#s/1/terminal"},
	{"agent", "#s/1/agent/a1f00dcafe0001"},
	{"job", "#s/1/job/214"},
	{"diff", "#s/1/diff"},
	{"mockup", "#s/7/mockup"},
	{"mockup-full", "#s/7/mockup/full"},
	{"flow", "#s/6/flow"},
	{"flow-story", "#s/6/flow/1"},
	{"flow-doc", "#s/6/flow/doc/spec?at=%C2%A72"},
	{"flows", "#flows"},
	{"flow-board", "#flow-board"},
	{"new", "#new"},
	{"settings", "#settings"},
	{"palette", "#s/1"},
	{"pair", "#pair"},
	{"add-to-home", ""},
}

// screenMode is how a screen's tab and canned loom differ from the Home
// Screen app on a paired loom. desktopOnly is a screen only the desktop layout
// draws, phoneOnly one only the phone's layout draws, and nextOnly one only
// the new app draws (the classic app has no such screen); press is a chord
// pressed once the desktop frame has drawn (Control+k, with Alt+, Control+,
// Meta+ and Shift+ prefixes allowed); ready is a selector that must match
// before the screen's click; shows is text the framed terminal's own text()
// must hold before the click (a copy sheet reads it once, as it opens) or, for a
// screen with no click, before its shot (the frame's "live" comes before xterm.js has
// parsed and painted the bytes); click
// is a selector clicked once, when it matches (and, when the screen has a
// ready, only after that matches); drawn is the selector that must match after
// the press or the click, or, for a screen with neither, before its shot. A screen
// with a shows and no click waits for that text before its shot too.
type screenMode struct {
	unpaired, browserTab, desktopOnly, phoneOnly, nextOnly bool
	press, ready, shows, click, drawn                      string
}

var screenModes = map[string]screenMode{
	"palette":         {desktopOnly: true, press: "Control+k", drawn: `[data-slot="palette"]`},
	"pair":            {unpaired: true},
	"add-to-home":     {browserTab: true},
	"terminal":        {nextOnly: true, shows: "0.412s", drawn: `[data-terminal-state="live"]`},
	"terminal-select": {phoneOnly: true, nextOnly: true, ready: `[data-terminal-state="live"]`, shows: "0.412s", click: `[data-slot="terminal-select"]`, drawn: `[data-slot="terminal-text"]:not(:empty)`},
}

// modeOf is the mode of a screen: the zero value for one that has none.
func modeOf(sc screen) screenMode { return screenModes[sc.name] }

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
	dev := fs.String("device", defaultDevice, "the device to render as: "+deviceNames())
	app := fs.String("app", "classic", "the app to render: classic (internal/web/static) or next (internal/web/dist)")
	static := fs.String("static", "", "the app's files (overrides the directory -app picks)")
	browserBin := fs.String("browser", "", "Chromium or Chrome to run (default: the first of chromium, chromium-browser, google-chrome, google-chrome-stable on PATH)")
	bare := fs.Bool("bare", false, "write the viewport only: no bezel, status bar, island or home indicator")
	guides := fs.Bool("guides", false, "tint the unsafe areas (the safe-area insets)")
	settle := fs.Duration("settle", 1200*time.Millisecond, "how long a screen gets to fetch and draw after it loads")
	serve := fs.String("serve", "", "serve the canned loom's /api/v1 on this address until killed, and render nothing (internal/web/ui/vite.config.ts proxies to 127.0.0.1:5670)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *serve != "" {
		if *out != "" || *only != "" {
			return errors.New("-serve renders nothing: drop -out and -screens")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serveAPI(ctx, *serve,
			func(a net.Addr) { fmt.Printf("serving the canned loom's /api/v1 on http://%s\n", a) },
			func(route string) {
				fmt.Fprintf(os.Stderr, "no answer for %s: add it to tools/webshot/fixture\n", route)
			})
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	d, ok := devices[*dev]
	if !ok {
		return fmt.Errorf("no device %q (have %s)", *dev, deviceNames())
	}
	todo, err := pickScreens(*only)
	if err != nil {
		return err
	}
	kept := forApp(*app, forDevice(d, todo))
	for _, sc := range todo {
		if !slices.Contains(kept, sc) {
			fmt.Printf("%s\tskipped: %s\n", sc.name, skipReason(d, *app, sc))
		}
	}
	if todo = kept; len(todo) == 0 {
		return fmt.Errorf("no screen to render on %s with -app %s", d.name, *app)
	}
	bin, err := findBrowser(*browserBin)
	if err != nil {
		return err
	}
	dir, err := appDir(*app, *static)
	if err != nil {
		return err
	}
	loom, err := newCannedLoom(dir)
	if err != nil {
		return fmt.Errorf("app %q: %w", *app, err)
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

	// A screen shot unpaired is served by a loom of its own, which refuses
	// every /api/ request as an unpaired device is refused.
	unpairedLoom, err := newCannedLoom(dir)
	if err != nil {
		return fmt.Errorf("app %q: %w", *app, err)
	}
	unpairedLoom.unpaired = true
	uln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	usrv := &http.Server{Handler: unpairedLoom, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = usrv.Serve(uln) }()
	defer usrv.Close()
	unpairedOrigin := "http://" + uln.Addr().String() + "/"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := launchBrowser(ctx, bin)
	if err != nil {
		return err
	}
	defer b.close()
	pg, err := b.openPage(ctx, d, true)
	if err != nil {
		return err
	}

	var bar image.Image
	if !*bare && !d.desktop {
		if bar, err = pg.statusBar(ctx, d); err != nil {
			return err
		}
	}

	failed := false
	for _, sc := range todo {
		file := filepath.Join(*out, sc.name+".png")
		// Each screen gets a tab of its own (see page.close).
		mode := modeOf(sc)
		tab, err := b.openPage(ctx, d, !mode.browserTab)
		if err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
		l, o := loom, origin
		if mode.unpaired {
			l, o = unpairedLoom, unpairedOrigin
		}
		problems, err := shoot(ctx, tab, l, d, bar, sc, o, *settle, file, frameOpts{bezel: !*bare, guides: *guides})
		tab.close(ctx)
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

// appDir is the directory of the files to serve: static when given, else the
// tree the app names, relative to the repo root.
func appDir(app, static string) (string, error) {
	switch app {
	case "", "classic", "next":
	default:
		return "", fmt.Errorf("no app %q (have classic, next)", app)
	}
	if static != "" {
		return static, nil
	}
	if app == "next" {
		return filepath.Join("internal", "web", "dist"), nil
	}
	return filepath.Join("internal", "web", "static"), nil
}

// serveAPI serves the canned /api/v1 (and no file) on addr until ctx is done,
// telling announce the address it listens on and onMiss of each route the
// fixture has no answer for. It stops with Close: the stream stays open for as
// long as a client holds it, and a Shutdown would wait on it.
func serveAPI(ctx context.Context, addr string, announce func(net.Addr), onMiss func(string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("serve %s: %w", addr, err)
	}
	announce(ln.Addr())
	loom := newCannedAPI()
	loom.onMiss = onMiss
	srv := &http.Server{Handler: loom.apiHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve %s: %w", addr, err)
	}
	return nil
}

// forDevice is todo as it is for a phone without the desktopOnly screens; for
// a desktop device, todo without the screens only a phone's browser tab draws
// (today add-to-home, the iOS Add to Home gate, shown only for an iOS user
// agent) or only the phone's layout draws (a phoneOnly screen). It tests
// modeOf's modes, not the screens' names, so a later screen of either kind is
// skipped too. todo is not changed.
func forDevice(d device, todo []screen) []screen {
	var out []screen
	for _, sc := range todo {
		m := modeOf(sc)
		if (d.desktop && (m.browserTab || m.phoneOnly)) || (!d.desktop && m.desktopOnly) {
			continue
		}
		out = append(out, sc)
	}
	return out
}

// forApp is todo, or without the desktopOnly and nextOnly screens unless app is
// next (the classic app has no desktop layout and no terminal, and the -app
// flag defaults to it). todo is not changed.
func forApp(app string, todo []screen) []screen {
	if app == "next" {
		return todo
	}
	var out []screen
	for _, sc := range todo {
		if m := modeOf(sc); !m.desktopOnly && !m.nextOnly {
			out = append(out, sc)
		}
	}
	return out
}

// skipReason is the words of a skip line for a screen forDevice or forApp
// dropped: the device's reason comes first, then the app's, and it is empty for
// a screen that is kept.
func skipReason(d device, app string, sc screen) string {
	m := modeOf(sc)
	switch {
	case d.desktop && (m.browserTab || m.phoneOnly):
		return "phone only"
	case !d.desktop && m.desktopOnly:
		return "desktop only"
	case (m.desktopOnly || m.nextOnly) && app != "next":
		return "next app only"
	}
	return ""
}

// deviceNames is the keys of devices, sorted and joined with ", ".
func deviceNames() string {
	names := make([]string, 0, len(devices))
	for n := range devices {
		names = append(names, n)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
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

// browserArgs is Chromium's command line for a run: headless, over the debugging pipe, with a
// throwaway profile. WebGL is off (--disable-3d-apis), so the terminal's frame draws with xterm.js's
// DOM renderer: a headless WebGL canvas can come out blank in a screenshot.
func browserArgs(profile string) []string {
	return []string{"--headless", "--remote-debugging-pipe", "--no-first-run", "--no-default-browser-check",
		"--disable-gpu", "--disable-3d-apis", "--hide-scrollbars", "--mute-audio", "--user-data-dir=" + profile, "about:blank"}
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
	cmd := exec.Command(bin, browserArgs(profile)...)
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
	b        *browser
	session  string
	targetID string
}

// openPage opens a tab and makes it the device's Home Screen app, or with
// standalone false an ordinary browser tab: its
// viewport, pixel ratio, touch, user agent and safe-area insets, with
// navigator.standalone set as iOS sets it (Chromium ignores an emulated
// display-mode), and with service workers bypassed so every load is the
// files on disk.
func (b *browser) openPage(ctx context.Context, d device, standalone bool) (*page, error) {
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
		// The tab exists: close it, or it keeps its connection slot.
		(&page{b: b, targetID: t.TargetID}).close(ctx)
		return nil, err
	}
	p := &page{b: b, session: a.SessionID, targetID: t.TargetID}
	for _, s := range emulation(d, standalone) {
		if err := b.cdp.call(ctx, p.session, s.method, s.params, nil); err != nil {
			p.close(ctx)
			return nil, err
		}
	}
	return p, nil
}

// close closes the tab, which ends its streams: a tab navigated away from
// is kept (for the back button) with its server-sent stream open, and six of
// them hold every connection Chromium allows the host, so the next screen's
// load waits about a minute for one.
func (p *page) close(ctx context.Context) {
	_ = p.b.cdp.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": p.targetID}, nil)
}

// cdpStep is one call that sets a tab up.
type cdpStep struct {
	method string
	params any
}

// emulation is the calls that make a tab the device's Home Screen app, or
// with standalone false its browser tab: the same but for navigator.standalone.
// A desktop device is no touch screen and has no insets: its viewport is not
// mobile, and the safe-area and touch steps are left out.
func emulation(d device, standalone bool) []cdpStep {
	standaloneScript := cdpStep{"Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": fmt.Sprintf(`Object.defineProperty(Navigator.prototype, "standalone", {get: () => %t, configurable: true});`, standalone),
	}}
	metrics := map[string]any{
		"width": d.width, "height": d.height, "deviceScaleFactor": d.scale, "mobile": !d.desktop,
		"screenWidth": d.width, "screenHeight": d.height,
	}
	platform := "Linux x86_64"
	if !d.desktop {
		metrics["screenOrientation"] = map[string]any{"type": "portraitPrimary", "angle": 0}
		platform = "iPhone"
	}
	steps := []cdpStep{
		{"Page.enable", nil},
		{"Runtime.enable", nil},
		{"Log.enable", nil},
		{"Network.enable", nil},
		{"Network.setBypassServiceWorker", map[string]any{"bypass": true}},
		{"Emulation.setDeviceMetricsOverride", metrics},
	}
	if !d.desktop {
		steps = append(steps,
			cdpStep{"Emulation.setSafeAreaInsetsOverride", map[string]any{"insets": map[string]any{
				"top": d.insetTop, "topMax": d.insetTop, "bottom": d.insetBottom, "bottomMax": d.insetBottom,
				"left": 0, "leftMax": 0, "right": 0, "rightMax": 0,
			}}},
			cdpStep{"Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}},
		)
	}
	return append(steps,
		cdpStep{"Emulation.setUserAgentOverride", map[string]any{"userAgent": d.userAgent, "platform": platform}},
		cdpStep{"Emulation.setScrollbarsHidden", map[string]any{"hidden": true}},
		standaloneScript,
	)
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

// parseChord splits a chord such as Control+k into its key and the CDP
// modifiers bitmask (Alt 1, Control 2, Meta 4, Shift 8). Every part but the last
// is a modifier, none repeats, and the last is one ASCII letter or digit.
func parseChord(chord string) (string, int, error) {
	bits := map[string]int{"Alt": 1, "Control": 2, "Meta": 4, "Shift": 8}
	parts := strings.Split(chord, "+")
	key := parts[len(parts)-1]
	mods := 0
	for _, m := range parts[:len(parts)-1] {
		bit, ok := bits[m]
		if !ok {
			return "", 0, fmt.Errorf("chord %q: %q is not a modifier (Alt, Control, Meta, Shift)", chord, m)
		}
		if mods&bit != 0 {
			return "", 0, fmt.Errorf("chord %q: %s is repeated", chord, m)
		}
		mods |= bit
	}
	if len(key) != 1 || !(key[0] >= 'a' && key[0] <= 'z' || key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9') {
		return "", 0, fmt.Errorf("chord %q: the key %q is not one ASCII letter or digit", chord, key)
	}
	return key, mods, nil
}

// press sends chord to the page once, as a key down and a key up. It types no
// character (a chord with Control or Meta types none, so there is no text); the
// page's own keydown listener is what a screen waits on.
func (p *page) press(ctx context.Context, chord string) error {
	key, mods, err := parseChord(chord)
	if err != nil {
		return err
	}
	up := strings.ToUpper(key)
	code := "Key" + up
	if key[0] >= '0' && key[0] <= '9' {
		code = "Digit" + key
	}
	for _, typ := range []string{"keyDown", "keyUp"} {
		params := map[string]any{
			"type": typ, "modifiers": mods, "key": key, "code": code, "windowsVirtualKeyCode": int(up[0]),
		}
		if err := p.b.cdp.call(ctx, p.session, "Input.dispatchKeyEvent", params, nil); err != nil {
			return fmt.Errorf("press %s: %w", chord, err)
		}
	}
	return nil
}

// waitFor polls every 100 ms until selector matches in the page or within has
// passed, and reports whether it matched. A query that errors counts as no
// match, as shoot's appDrawn loop treats it.
func (p *page) waitFor(ctx context.Context, selector string, within time.Duration) bool {
	q, _ := json.Marshal(selector)
	return p.waitExpr(ctx, "!!document.querySelector("+string(q)+")", within)
}

// waitExpr polls every 100 ms until expr, a page expression that answers a boolean, answers true
// or within has passed, and reports whether it did. A query that errors counts as no match.
func (p *page) waitExpr(ctx context.Context, expr string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		var ok bool
		if err := p.query(ctx, expr, &ok); err == nil && ok {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pressWait is how long a screen with a chord gets for the desktop frame, and
// then for its slot, to appear.
const pressWait = 5 * time.Second

// desktopFrame is the selector of the frame the lazy DesktopShell draws:
// appDrawn is already true on the phone shell's splash and on the lazy shell's
// fallback, where the chord has no listener yet.
const desktopFrame = `[data-slot="desktop"]`

// pressScreen presses mode's chord once the desktop frame has drawn and
// settled, then waits for the slot the chord opens. It does nothing for a mode
// with no chord. The chord toggles, so it is pressed once and never retried.
func (p *page) pressScreen(ctx context.Context, mode screenMode, settle, wait time.Duration) error {
	if mode.press == "" {
		return nil
	}
	if !p.waitFor(ctx, desktopFrame, wait) {
		return fmt.Errorf("no desktop frame to press %s on", mode.press)
	}
	time.Sleep(settle)
	if err := p.press(ctx, mode.press); err != nil {
		return err
	}
	if !p.waitFor(ctx, mode.drawn, wait) {
		return fmt.Errorf("pressing %s drew no %s", mode.press, mode.drawn)
	}
	return nil
}

// drawnWait is how long a screen that waits for its own state (a live terminal, then a sheet)
// gets for each selector to match: the frame has to load xterm.js and connect first.
const drawnWait = 10 * time.Second

// waits reports whether the screen waits for its own state once the app has drawn, with no chord:
// for a drawn selector, after a click when it has one.
func (m screenMode) waits() bool {
	return m.press == "" && (m.click != "" || m.drawn != "")
}

// drawScreen waits for mode's own state: its ready selector first when it has one, then the
// text the framed terminal must hold when it has one (a sheet reads the text once, so a click
// before the terminal has parsed its first bytes would open an empty one), then, for a screen
// with a click, the click's selector, which it clicks once, and then the drawn selector.
// It does nothing for a screen with a chord (pressScreen's) or with none of these. The click
// is never retried.
func (p *page) drawScreen(ctx context.Context, mode screenMode, wait time.Duration) error {
	if !mode.waits() {
		return nil
	}
	if mode.ready != "" && !p.waitFor(ctx, mode.ready, wait) {
		return fmt.Errorf("no %s to wait for", mode.ready)
	}
	if mode.shows != "" && !p.waitShows(ctx, mode.shows, wait) {
		return fmt.Errorf("the terminal never showed %q", mode.shows)
	}
	if mode.click != "" {
		if !p.waitFor(ctx, mode.click, wait) {
			return fmt.Errorf("no %s to click", mode.click)
		}
		if err := p.click(ctx, mode.click); err != nil {
			return err
		}
		if !p.waitFor(ctx, mode.drawn, wait) {
			return fmt.Errorf("clicking %s drew no %s", mode.click, mode.drawn)
		}
		return nil
	}
	if !p.waitFor(ctx, mode.drawn, wait) {
		return fmt.Errorf("no %s drew", mode.drawn)
	}
	return nil
}

// waitShows polls until the framed terminal's text() holds text. The framing page reads it through
// the frame's same-origin window, as the app's panel does (Story 3's window.loomTerminal, which
// answers "" until the terminal has started). A frame not yet there, or a script that throws,
// counts as no match, as waitFor treats a query that errors.
func (p *page) waitShows(ctx context.Context, text string, within time.Duration) bool {
	q, _ := json.Marshal(text)
	expr := `(() => { const f = document.querySelector('iframe[src^="/terminal.html"]'); ` +
		`const t = f && f.contentWindow && f.contentWindow.loomTerminal; ` +
		`return !!t && t.text().includes(` + string(q) + `); })()`
	return p.waitExpr(ctx, expr, within)
}

// click clicks the first element selector matches.
func (p *page) click(ctx context.Context, selector string) error {
	q, _ := json.Marshal(selector)
	expr := "(() => { const el = document.querySelector(" + string(q) + "); " +
		"if (el === null) return false; el.click(); return true; })()"
	var ok bool
	if err := p.query(ctx, expr, &ok); err != nil {
		return fmt.Errorf("click %s: %w", selector, err)
	}
	if !ok {
		return fmt.Errorf("click %s: nothing matches", selector)
	}
	return nil
}

// appDrawn is true once the page is the app and it has drawn into #app.
const appDrawn = `document.readyState === "complete" && location.protocol === "http:" && ` +
	`!!document.querySelector("#app") && document.querySelector("#app").childElementCount > 0`

// shoot loads one screen fresh, waits until the app has drawn and settled,
// captures the viewport and writes it framed to file, bar laid over it. A screen
// with a chord is pressed after the frame has drawn and settled, and settles
// again before the capture; one with a drawn selector and no chord waits for it,
// after its click when it has one, and settles again. It returns what went
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
	mode := modeOf(sc)
	if mode.press != "" {
		if err := p.pressScreen(ctx, mode, settle, pressWait); err != nil {
			return nil, err
		}
		time.Sleep(settle)
	}
	if mode.waits() {
		if err := p.drawScreen(ctx, mode, drawnWait); err != nil {
			return nil, err
		}
		time.Sleep(settle)
	}

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
