package main

import (
	"image"
	"image/color"
	"math"
)

// device is a phone's geometry in CSS points, as a Home Screen app sees it:
// the viewport is the whole screen (viewport-fit=cover with a black-translucent
// status bar), and the safe-area insets are what env(safe-area-inset-*)
// resolves to in portrait.
type device struct {
	name          string
	width, height int     // the viewport, in points
	scale         float64 // device pixels per point
	insetTop      int     // the status bar and the Dynamic Island
	insetBottom   int     // the home indicator
	corner        float64 // the display's corner radius
	island        rectPt  // the Dynamic Island
	homeBar       rectPt  // the home indicator
	userAgent     string
}

// rectPt is a rectangle in points, its corners rounded by r.
type rectPt struct{ x, y, w, h, r float64 }

// devices are the targets the tool can render. The iPhone 16 is the owner's
// phone: 393×852 points at @3x (1179×2556 pixels), safe area 59 over 34 in
// portrait, the Dynamic Island 126×37 points 11 from the top, the home
// indicator 139×5 points 8 from the bottom.
var devices = map[string]device{
	"iphone16": {
		name:        "iPhone 16",
		width:       393,
		height:      852,
		scale:       3,
		insetTop:    59,
		insetBottom: 34,
		corner:      55,
		island:      rectPt{x: (393 - 126) / 2.0, y: 11, w: 126, h: 37, r: 18.5},
		homeBar:     rectPt{x: (393 - 139) / 2.0, y: 852 - 8 - 5, w: 139, h: 5, r: 2.5},
		userAgent:   "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
	},
}

// frameOpts is what frame draws over a screenshot.
type frameOpts struct {
	bezel  bool // a black bezel round the screen, and the screen's rounded corners
	guides bool // tint the unsafe areas, where nothing the user taps may sit
}

// bezelPt is the bezel's width round the screen, in points.
const bezelPt = 14

var (
	inkBlack  = color.NRGBA{0, 0, 0, 255}
	inkWhite  = color.NRGBA{255, 255, 255, 255}
	inkHome   = color.NRGBA{255, 255, 255, 220}
	inkGuide  = color.NRGBA{255, 40, 90, 70}
	inkBezel  = color.NRGBA{12, 12, 14, 255}
	inkBezelE = color.NRGBA{58, 58, 62, 255}
)

// frame draws the device over shot, a screenshot of its whole viewport at
// its scale: the guides first, then the status bar, the Dynamic Island and
// the home indicator, as iOS draws them over a Home Screen app; with a bezel,
// the screen's corners are cut and a bezel is drawn round it. The result is
// a new image; shot is not changed.
func frame(shot image.Image, d device, o frameOpts) *image.NRGBA {
	s := d.scale
	pad := 0
	if o.bezel {
		pad = int(math.Round(bezelPt * s))
	}
	b := shot.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx()+2*pad, b.Dy()+2*pad))

	screen := rectPx{x: float64(pad), y: float64(pad), w: float64(b.Dx()), h: float64(b.Dy()), r: d.corner * s}
	if o.bezel {
		outer := rectPx{x: 0, y: 0, w: float64(out.Bounds().Dx()), h: float64(out.Bounds().Dy()), r: screen.r + float64(pad)}
		fillRound(out, outer, inkBezelE)
		edge := 2 * s
		fillRound(out, rectPx{outer.x + edge, outer.y + edge, outer.w - 2*edge, outer.h - 2*edge, outer.r - edge}, inkBezel)
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			a := 1.0
			if o.bezel {
				a = coverage(screen, float64(pad+x)+0.5, float64(pad+y)+0.5)
			}
			if a <= 0 {
				continue
			}
			c := color.NRGBAModel.Convert(shot.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			blend(out, pad+x, pad+y, c, a)
		}
	}

	at := func(r rectPt) rectPx {
		return rectPx{x: float64(pad) + r.x*s, y: float64(pad) + r.y*s, w: r.w * s, h: r.h * s, r: r.r * s}
	}
	if o.guides {
		clip := &screen
		if !o.bezel {
			clip = nil
		}
		fillRoundIn(out, at(rectPt{w: float64(d.width), h: float64(d.insetTop)}), inkGuide, clip)
		fillRoundIn(out, at(rectPt{y: float64(d.height - d.insetBottom), w: float64(d.width), h: float64(d.insetBottom)}), inkGuide, clip)
	}
	drawStatusBar(out, d, float64(pad))
	fillRound(out, at(d.island), inkBlack)
	fillRound(out, at(d.homeBar), inkHome)
	return out
}

// drawStatusBar draws the time left of the Dynamic Island and the signal and
// battery right of it, each centred in its side and on the island's middle.
func drawStatusBar(out *image.NRGBA, d device, pad float64) {
	s := d.scale
	mid := d.island.y + d.island.h/2
	leftC := d.island.x / 2
	rightC := d.island.x + d.island.w + (float64(d.width)-d.island.x-d.island.w)/2

	// "9:41" in a 5×7 dot font, its dots 1.8 points square.
	dot := 1.8
	text := "9:41"
	wPt := 0.0
	for i, r := range text {
		wPt += float64(glyphs[r].w) * dot
		if i < len(text)-1 {
			wPt += dot
		}
	}
	x := leftC - wPt/2
	y := mid - 3.5*dot
	for _, r := range text {
		g := glyphs[r]
		for row, line := range g.rows {
			for col, ch := range line {
				if ch == '#' {
					fillRound(out, rectPx{pad + (x+float64(col)*dot)*s, pad + (y+float64(row)*dot)*s, dot * s, dot * s, 0}, inkWhite)
				}
			}
		}
		x += float64(g.w)*dot + dot
	}

	// Four signal bars, then the battery: 25×12 points, its cap and charge.
	bars := 4
	barW, gap := 3.0, 1.5
	batW, batH := 25.0, 12.0
	group := float64(bars)*barW + float64(bars-1)*gap + 7 + batW + 2
	x = rightC - group/2
	for i := 0; i < bars; i++ {
		h := 4 + float64(i)*2.4
		fillRound(out, rectPx{pad + x*s, pad + (mid+5.5-h)*s, barW * s, h * s, 0.8 * s}, inkWhite)
		x += barW + gap
	}
	x += 7 - gap
	top := mid - batH/2
	fillRound(out, rectPx{pad + x*s, pad + top*s, batW * s, batH * s, 3.8 * s}, color.NRGBA{255, 255, 255, 110})
	fillRound(out, rectPx{pad + (x+2)*s, pad + (top+2)*s, (batW - 4) * 0.8 * s, (batH - 4) * s, 2 * s}, inkWhite)
	fillRound(out, rectPx{pad + (x+batW+1)*s, pad + (mid-2)*s, 1.5 * s, 4 * s, 0.7 * s}, color.NRGBA{255, 255, 255, 110})
}

// glyph is one character of the status bar's dot font.
type glyph struct {
	w    int
	rows [7]string
}

var glyphs = map[rune]glyph{
	'9': {5, [7]string{".###.", "#...#", "#...#", ".####", "....#", "...#.", ".##.."}},
	'4': {5, [7]string{"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."}},
	'1': {3, [7]string{".#.", "##.", ".#.", ".#.", ".#.", ".#.", "###"}},
	':': {1, [7]string{".", "#", ".", ".", ".", "#", "."}},
}

// rectPx is a rectangle in pixels, its corners rounded by r.
type rectPx struct{ x, y, w, h, r float64 }

// coverage is how much of the pixel centred on (px, py) the rounded
// rectangle covers, 0 to 1, from its signed distance: a one-pixel ramp at the
// edge is enough anti-aliasing for a mockup.
func coverage(r rectPx, px, py float64) float64 {
	cx, cy := r.x+r.w/2, r.y+r.h/2
	rad := math.Min(r.r, math.Min(r.w, r.h)/2)
	qx := math.Abs(px-cx) - (r.w/2 - rad)
	qy := math.Abs(py-cy) - (r.h/2 - rad)
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - rad
	return math.Max(0, math.Min(1, 0.5-d))
}

// fillRound blends c over out wherever the rounded rectangle r covers it.
func fillRound(out *image.NRGBA, r rectPx, c color.NRGBA) { fillRoundIn(out, r, c, nil) }

// fillRoundIn is fillRound kept inside clip, when clip is not nil.
func fillRoundIn(out *image.NRGBA, r rectPx, c color.NRGBA, clip *rectPx) {
	b := out.Bounds()
	x0, y0 := max(b.Min.X, int(math.Floor(r.x))), max(b.Min.Y, int(math.Floor(r.y)))
	x1, y1 := min(b.Max.X, int(math.Ceil(r.x+r.w))), min(b.Max.Y, int(math.Ceil(r.y+r.h)))
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			a := coverage(r, float64(x)+0.5, float64(y)+0.5)
			if clip != nil {
				a *= coverage(*clip, float64(x)+0.5, float64(y)+0.5)
			}
			if a > 0 {
				blend(out, x, y, c, a)
			}
		}
	}
}

// blend draws c over the pixel at (x, y) with its own alpha scaled by a.
func blend(out *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	src := float64(c.A) / 255 * a
	dst := out.NRGBAAt(x, y)
	da := float64(dst.A) / 255
	oa := src + da*(1-src)
	if oa <= 0 {
		return
	}
	mix := func(s, d uint8) uint8 {
		return uint8(math.Round((float64(s)*src + float64(d)*da*(1-src)) / oa))
	}
	out.SetNRGBA(x, y, color.NRGBA{mix(c.R, dst.R), mix(c.G, dst.G), mix(c.B, dst.B), uint8(math.Round(oa * 255))})
}
