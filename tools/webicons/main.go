// Command webicons draws the web app's icons into internal/web/static/icons.
//
// Run it from the repo root: go run ./tools/webicons. The icons are
// committed, so a build needs no step; run it again only to change the
// drawing. The drawing is a small weave in loom's default theme (afterglow):
// the accent warp and the workspace-colour weft on the ground colour, inside
// the middle three fifths of the square so a mask can round it.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// sizes are the icon edges the manifest and apple-touch-icon use.
var sizes = []int{180, 192, 512}

// threads is the number of warp threads and of weft threads.
const threads = 5

var (
	ground = color.RGBA{R: 0x2e, G: 0x2e, B: 0x2e, A: 0xff}
	warp   = color.RGBA{R: 0x6c, G: 0x99, B: 0xbb, A: 0xff}
	weft   = color.RGBA{R: 0xa1, G: 0x61, B: 0x7a, A: 0xff}
)

// drawIcon draws one square icon: horizontal threads, vertical threads, then
// the weft repainted over the warp at every other crossing.
func drawIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := func(c color.RGBA, x0, y0, x1, y1 int) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(ground, 0, 0, size, size)

	margin := size / 5
	pitch := (size - 2*margin) / threads
	thick := pitch * 3 / 5
	if thick < 1 {
		thick = 1
	}
	span := pitch * threads
	origin := (size - span) / 2
	at := func(i int) int { return origin + i*pitch + (pitch-thick)/2 }

	for j := 0; j < threads; j++ {
		fill(weft, origin, at(j), origin+span, at(j)+thick)
	}
	for i := 0; i < threads; i++ {
		fill(warp, at(i), origin, at(i)+thick, origin+span)
	}
	for i := 0; i < threads; i++ {
		for j := 0; j < threads; j++ {
			if (i+j)%2 == 1 {
				fill(weft, at(i), at(j), at(i)+thick, at(j)+thick)
			}
		}
	}
	return img
}

// writeIcon writes icon-<size>.png into dir.
func writeIcon(dir string, size int) error {
	name := filepath.Join(dir, fmt.Sprintf("icon-%d.png", size))
	f, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, drawIcon(size)); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	return nil
}

func main() {
	out := flag.String("out", filepath.Join("internal", "web", "static", "icons"), "directory to write the icons into")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "webicons:", err)
		os.Exit(1)
	}
	for _, size := range sizes {
		if err := writeIcon(*out, size); err != nil {
			fmt.Fprintln(os.Stderr, "webicons:", err)
			os.Exit(1)
		}
	}
}
