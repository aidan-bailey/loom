package web

import (
	"fmt"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIcons_AreOpaqueDrawingsOfTheirSize(t *testing.T) {
	for _, size := range []int{180, 192, 512} {
		name := fmt.Sprintf("static/icons/icon-%d.png", size)
		f, err := os.Open(name)
		require.NoError(t, err, name)
		img, err := png.Decode(f)
		require.NoError(t, f.Close())
		require.NoError(t, err, name)

		b := img.Bounds()
		require.Equal(t, size, b.Dx(), name)
		require.Equal(t, size, b.Dy(), name)

		for _, p := range [][2]int{{0, 0}, {size - 1, 0}, {0, size - 1}, {size - 1, size - 1}} {
			_, _, _, a := img.At(p[0], p[1]).RGBA()
			require.Equal(t, uint32(0xffff), a, "%s corner %v is not opaque", name, p)
		}

		colours := map[color.Color]bool{}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				colours[img.At(x, y)] = true
			}
		}
		require.GreaterOrEqual(t, len(colours), 3, "%s is a blank square", name)
	}
}
