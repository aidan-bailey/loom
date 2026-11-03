package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"time"
)

// statusBarHTML is iOS's status bar for d as a page: the time centred left of
// the Dynamic Island, the signal, Wi-Fi and battery centred right of it, all
// on the island's middle and on a transparent ground. The browser draws it,
// so the time is set in a real font and the icons are vectors, sharp at any
// scale; the tool's own pixel drawing is kept to shapes.
func statusBarHTML(d device) string {
	right := d.island.x + d.island.w
	return fmt.Sprintf(`<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<style>
html, body { margin: 0; background: transparent; }
.side { position: fixed; top: %[1]gpx; height: %[2]gpx; display: flex; align-items: center; justify-content: center; color: #fff; }
.time { left: 0; width: %[3]gpx; font: 600 17px/1 "SF Pro Text", -apple-system, Inter, "Noto Sans", "Helvetica Neue", Arial, sans-serif; letter-spacing: -0.3px; }
.icons { left: %[4]gpx; right: 0; gap: 6px; }
svg { display: block; fill: #fff; }
</style>
<div class="side time">9:41</div>
<div class="side icons">
<svg width="18" height="12" viewBox="0 0 18 12"><rect x="0" y="7.5" width="3" height="4.5" rx="1"/><rect x="5" y="5" width="3" height="7" rx="1"/><rect x="10" y="2.5" width="3" height="9.5" rx="1"/><rect x="15" y="0" width="3" height="12" rx="1"/></svg>
<svg width="16" height="12" viewBox="0 0 16 12"><path d="M8 2.2c2.4 0 4.6.9 6.3 2.5l1.2-1.3A10.8 10.8 0 0 0 8 .4 10.8 10.8 0 0 0 .5 3.4l1.2 1.3A9 9 0 0 1 8 2.2Z"/><path d="M8 5.6c1.5 0 2.9.6 4 1.5l1.2-1.3A7.6 7.6 0 0 0 8 3.8a7.6 7.6 0 0 0-5.2 2l1.2 1.3c1.1-.9 2.5-1.5 4-1.5Z"/><path d="M8 9 10.6 8.4A3.9 3.9 0 0 0 8 7.3a3.9 3.9 0 0 0-2.6 1.1L8 11.6l2.6-3.2Z"/></svg>
<svg width="27" height="13" viewBox="0 0 27 13"><rect x="0.5" y="0.5" width="23" height="12" rx="3.8" fill="none" stroke="#fff" stroke-opacity="0.4"/><rect x="2" y="2" width="20" height="9" rx="2.4"/><path d="M25 4.5v4a2 2 0 0 0 0-4Z" fill-opacity="0.45"/></svg>
</div>`, d.island.y, d.island.h, d.island.x, right)
}

// statusBar has the page draw d's status bar on a transparent ground and
// returns it at the viewport's size, for frame to lay over every screen.
func (p *page) statusBar(ctx context.Context, d device) (image.Image, error) {
	call := func(method string, params any) error { return p.b.cdp.call(ctx, p.session, method, params, nil) }
	if err := call("Emulation.setDefaultBackgroundColorOverride", map[string]any{"color": map[string]any{"r": 0, "g": 0, "b": 0, "a": 0}}); err != nil {
		return nil, err
	}
	// Without a colour the override is cleared: the app's pages draw their own
	// ground, and an emptied one must not show through as transparency.
	defer func() { _ = call("Emulation.setDefaultBackgroundColorOverride", nil) }()

	url := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(statusBarHTML(d)))
	if err := call("Page.navigate", map[string]any{"url": url}); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ready bool
		if err := p.query(ctx, `document.readyState === "complete" && location.protocol === "data:" && document.fonts.status === "loaded"`, &ready); err == nil && ready {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the status bar did not draw in 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	img, err := p.capture(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("the status bar: %w", err)
	}
	return img, nil
}

// capture is the viewport as a PNG at the device's pixel size.
func (p *page) capture(ctx context.Context, d device) (image.Image, error) {
	var shot struct {
		Data string `json:"data"`
	}
	if err := p.b.cdp.call(ctx, p.session, "Page.captureScreenshot", map[string]any{"format": "png"}, &shot); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	want := [2]int{int(float64(d.width) * d.scale), int(float64(d.height) * d.scale)}
	if got := [2]int{img.Bounds().Dx(), img.Bounds().Dy()}; got != want {
		return nil, fmt.Errorf("the screenshot is %dx%d, not the device's %dx%d", got[0], got[1], want[0], want[1])
	}
	return img, nil
}
