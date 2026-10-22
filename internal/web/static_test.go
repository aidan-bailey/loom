package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// The vendored module is htm 3.1.1's preact/standalone.module.js, byte for
// byte after a one-line header comment.
const (
	preactHTMURL    = "https://unpkg.com/htm@3.1.1/preact/standalone.module.js"
	preactHTMSize   = 13194
	preactHTMSHA256 = "72284e8e9079c87817145df1110f74e8a2aa040b2fc384922e18dfcb46fc1fd7"
)

func TestPreactHTM_IsThePinnedFile(t *testing.T) {
	raw, err := os.ReadFile("static/lib/preact-htm.mjs")
	require.NoError(t, err)

	header, body, ok := bytes.Cut(raw, []byte("\n"))
	require.True(t, ok, "the file is one line")
	require.True(t, bytes.HasPrefix(header, []byte("//")), "the first line is a comment")
	for _, want := range []string{"htm 3.1.1", preactHTMURL, preactHTMSHA256, "NOTICE.md"} {
		require.Contains(t, string(header), want)
	}

	require.Len(t, body, preactHTMSize)
	sum := sha256.Sum256(body)
	require.Equal(t, preactHTMSHA256, hex.EncodeToString(sum[:]))
}
