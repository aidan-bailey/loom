package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWebListen(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7420", "[::1]:7420", "127.0.0.1:1", "127.0.0.1:65535", "127.0.0.2:8080"} {
		assert.NoError(t, ValidateWebListen(listen), listen)
	}
	for _, listen := range []string{
		"", "localhost:7420", "0.0.0.0:7420", ":7420", "192.168.1.5:7420", "[::]:7420", "[fe80::1]:7420",
		"127.0.0.1", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:-1", "127.0.0.1:+80",
		"127.0.0.1:07420", "127.0.0.1:http", "[127.0.0.1]:7420", "[0:0:0:0:0:0:0:1]:7420",
		"[::ffff:127.0.0.1]:7420", "[::1%lo]:7420", "::1:7420", " 127.0.0.1:7420", "127.0.0.1:7420 ",
		"127.0.0.1:7420/x", "unix:/tmp/loom.sock", "/tmp/loom.sock",
	} {
		assert.Error(t, ValidateWebListen(listen), "%q", listen)
	}
	assert.ErrorContains(t, ValidateWebListen("localhost:7420"), "loopback IP literal", "a host name is refused for what it is")
}

func TestValidateWebOrigin(t *testing.T) {
	long := "https://" + strings.Repeat("a", maxWebOrigin-len("https://"))
	for _, origin := range []string{
		"https://loom.example.com", "https://loom.example.com:8443", "https://x",
		"http://127.0.0.1:7420", "http://localhost:3000", long,
	} {
		assert.NoError(t, ValidateWebOrigin(origin), origin)
	}
	for _, origin := range []string{
		"", "loom.example.com", "//loom.example.com", "ftp://loom.example.com", "https://", "https:///x",
		"https://loom.example.com/", "https://loom.example.com/app", "https://loom.example.com?",
		"https://loom.example.com?a=1", "https://loom.example.com#", "https://loom.example.com#top",
		"https://u@loom.example.com", "https://u:p@loom.example.com", "HTTPS://loom.example.com",
		"https://Loom.Example.com", "https://loom.example.com:", "https://loom.example.com:0",
		"https://loom.example.com:65536", "https://loom.example.com:08443", "https://loom.example.com:+1",
		" https://loom.example.com", "https://loom.example.com ", "https://lo om.example.com",
		"http://loom.example.com:80", "http://localhost", "http://127.0.0.1", "http://[::1]:7420",
		"https:loom.example.com", long + "a",
	} {
		assert.Error(t, ValidateWebOrigin(origin), "%q", origin)
	}
}

func TestValidateDeviceName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"iPhone", "iPhone"},
		{"  iPhone \t", "iPhone"},
		{"Aidan's iPhone 15", "Aidan's iPhone 15"},
		{"x", "x"},
		{strings.Repeat("x", 32), strings.Repeat("x", 32)},
		{strings.Repeat("é", 32), strings.Repeat("é", 32)},
	} {
		got, err := ValidateDeviceName(tc.in)
		require.NoError(t, err, "%q", tc.in)
		assert.Equal(t, tc.want, got)
	}
	for _, in := range []string{
		"", "   ", "\t\n", strings.Repeat("x", 33), strings.Repeat("é", 33),
		"a\tb", "a\nb", "bad\x00", "\x7f", "a\xffb",
	} {
		_, err := ValidateDeviceName(in)
		assert.Error(t, err, "%q", in)
	}
}

func TestNormalizePairingCode(t *testing.T) {
	for in, want := range map[string]string{
		"K7QM-2XPA":       "K7QM2XPA",
		"k7qm2xpa":        "K7QM2XPA",
		" k7qm 2xpa ":     "K7QM2XPA",
		"K-7-Q-M-2-X-P-A": "K7QM2XPA",
		"K7QM2XPA":        "K7QM2XPA",
	} {
		got, ok := NormalizePairingCode(in)
		assert.True(t, ok, "%q", in)
		assert.Equal(t, want, got, "%q", in)
	}
	for _, in := range []string{
		"", "----", "K7QM2XP", "K7QM2XPAA", "K7QM-2XPA-K7QM",
		"K7QM2XP0", "K7QM2XP1", "K7QMIXPA", "K7QM2XPO", "K7QM2XPL",
		"K7QM2XPÁ", "K7QM2XP\t", "ſ7QM2XPA",
	} {
		got, ok := NormalizePairingCode(in)
		assert.False(t, ok, "%q", in)
		assert.Empty(t, got, "%q", in)
	}
}

func TestPairingAlphabet_HasNoLookAlikes(t *testing.T) {
	assert.Len(t, PairingAlphabet, 31)
	seen := map[rune]bool{}
	for _, r := range PairingAlphabet {
		assert.False(t, seen[r], "%q twice", r)
		seen[r] = true
	}
	for _, r := range "01ILO" {
		assert.NotContains(t, PairingAlphabet, string(r))
	}
}

func TestPushFailing(t *testing.T) {
	assert.Equal(t, PushState("failing: 503"), PushFailing(503))
	assert.Equal(t, PushState("failing: 0"), PushFailing(0))
}
