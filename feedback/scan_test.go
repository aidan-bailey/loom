package feedback

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fmtHits renders hits the way `loom feedback scan` prints them.
func fmtHits(hits []ScanHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%d:%d %s: %s", h.Line, h.Col, h.Kind, h.Match))
	}
	return out
}

// scanUUID is a UUID, as a Claude session ID is spelled.
const scanUUID = "7f9c1e2a-3b4d-4e5f-8a6b-1c2d3e4f5a6b"

func TestScan_EachCredentialShapeIsAHit(t *testing.T) {
	// Built from parts, so no literal in this file is shaped like a credential.
	cases := map[string]string{
		"github pat":   "github" + "_pat_" + strings.Repeat("A", 22),
		"slack":        "xox" + "b-" + strings.Repeat("1", 12),
		"aws":          "AKIA" + strings.Repeat("A", 16),
		"an api key":   "sk" + "-" + strings.Repeat("a", 24),
		"private key":  "-----BEGIN " + "RSA PRIVATE KEY-----",
		"bare key":     "-----BEGIN " + "PRIVATE KEY-----",
		"openssh key":  "-----BEGIN " + "OPENSSH PRIVATE KEY-----",
		"slack legacy": "xox" + "p-" + strings.Repeat("1", 12),
	}
	for _, k := range "pousr" {
		cases["github "+string(k)] = "gh" + string(k) + "_" + strings.Repeat("a", 36)
	}
	for name, tok := range cases {
		hits, allowed := Scan("export X="+tok+"\n", ScanTerms{}, nil)
		assert.Equal(t, []string{"1:10 token: " + tok}, fmtHits(hits), name)
		assert.Empty(t, allowed, name)
	}
}

func TestScan_NearMissesAreNotCredentials(t *testing.T) {
	for name, text := range map[string]string{
		"a word that ends in sk-":       "ask-" + strings.Repeat("a", 24),
		"a short github token":          "ghp_abc",
		"a short fine-grained token":    "github" + "_pat_abc",
		"a short slack token":           "xox" + "b-123",
		"a short api key":               "sk-abc",
		"a slack token of another kind": "xoxz-" + strings.Repeat("1", 12),
		"an aws key one short":          "AKIA" + strings.Repeat("A", 15),
		"an aws key after a letter":     "xAKIA" + strings.Repeat("A", 16),
		"a certificate":                 "-----BEGIN CERTIFICATE-----",
	} {
		hits, _ := Scan(text, ScanTerms{}, nil)
		assert.Empty(t, hits, name)
	}
}

func TestScan_EmailsAndUUIDsAreHits(t *testing.T) {
	hits, _ := Scan("mail me at bob.smith+x@example.co.uk.\nid "+scanUUID+" done\nUPPER "+strings.ToUpper(scanUUID)+"\n", ScanTerms{}, nil)
	assert.Equal(t, []string{
		"1:12 email: bob.smith+x@example.co.uk",
		"2:4 uuid: " + scanUUID,
		"3:7 uuid: " + strings.ToUpper(scanUUID),
	}, fmtHits(hits))

	for name, text := range map[string]string{
		"an address with no domain dot": "a@b",
		"a scoped package":              "@types/node",
		"a uuid one group short":        "7f9c1e2a-3b4d-4e5f-8a6b",
	} {
		hits, _ := Scan(text, ScanTerms{}, nil)
		assert.Empty(t, hits, name)
	}
}

func TestScan_LinesAndColumnsAreOneBasedAndCountCharacters(t *testing.T) {
	// "élan " is five characters and six bytes: the column is the character's.
	// The draft mixes CRLF and LF breaks, and its first hit ends a CRLF line.
	hits, _ := Scan("ok\r\nélan "+scanUUID+"\r\n\nlast "+scanUUID, ScanTerms{}, nil)
	assert.Equal(t, []string{"2:6 uuid: " + scanUUID, "4:6 uuid: " + scanUUID}, fmtHits(hits))
}

func TestScan_AllowDropsAHitWhoseWholeMatchIsTheWord(t *testing.T) {
	tok := "gh" + "p_" + strings.Repeat("a", 36)
	text := "tok " + tok + "\nmail bob@example.com\n"

	hits, allowed := Scan(text, ScanTerms{}, []string{"BOB@example.com"})
	assert.Equal(t, []string{"1:5 token: " + tok}, fmtHits(hits))
	assert.Equal(t, []string{"2:6 email: bob@example.com"}, fmtHits(allowed), "any case, and reported as dropped")

	hits, allowed = Scan(text, ScanTerms{}, []string{"bob", "example.com", ""})
	assert.Len(t, hits, 2, "a word inside a match drops nothing")
	assert.Empty(t, allowed)
}

func TestScan_WorkspaceNamesAreWholeWordsInAnyCase(t *testing.T) {
	terms := ScanTerms{Workspaces: []string{"polyoxide", "my-repo", ""}}
	hits, _ := Scan("Polyoxide's tests failed in my-repo\npolyoxides, xpolyoxide, polyoxide_api, my-repository\n(POLYOXIDE)\n", terms, nil)
	assert.Equal(t, []string{
		"1:1 workspace: Polyoxide",
		"1:29 workspace: my-repo",
		"3:2 workspace: POLYOXIDE",
	}, fmtHits(hits))
}

func TestScan_SessionTitlesAreWholeWordSequences(t *testing.T) {
	terms := ScanTerms{Sessions: []string{"fix login bug"}}
	hits, _ := Scan("I saw Fix  Login\tbug today\nfix login\nprefix login bug\nfix login bugs\nFIX LOGIN BUG.\n", terms, nil)
	assert.Equal(t, []string{
		"1:7 session: Fix  Login\tbug",
		"5:1 session: FIX LOGIN BUG",
	}, fmtHits(hits))
}

func TestScan_IdentityTermsAreWholeWordsAndAnEmailIsNotAlsoAnEmailHit(t *testing.T) {
	terms := ScanTerms{Identity: []string{"alice", "fresco", "alice@example.com"}}
	hits, _ := Scan("alice@example.com wrote from FRESCO\nalicex and xalice are other people\n", terms, nil)
	assert.Equal(t, []string{
		"1:1 identity: alice",
		"1:1 identity: alice@example.com",
		"1:30 identity: FRESCO",
	}, fmtHits(hits))
}

func TestScan_TheThreadsTermsAreThreadHits(t *testing.T) {
	terms := ScanTerms{
		Thread:      []string{scanUUID, "/home/x/.claude/projects/p/s.jsonl", "me/secret-branch"},
		ThreadNames: []string{"Deploy Nightly", "gone-ws"},
	}
	other := "11111111-2222-3333-4444-555555555555"
	text := "session " + scanUUID + " other " + other + "\n" +
		"at /home/x/.claude/projects/p/s.jsonl\n" +
		"on branch me/secret-branch and deploy nightly in GONE-WS\n"
	hits, _ := Scan(text, terms, nil)
	assert.Equal(t, []string{
		"1:9 thread: " + scanUUID,
		"1:52 uuid: " + other,
		"2:4 thread: /home/x/.claude/projects/p/s.jsonl",
		"3:11 thread: me/secret-branch",
		"3:32 thread: deploy nightly",
		"3:50 thread: GONE-WS",
	}, fmtHits(hits))
}

func TestScan_OneSpanIsOneHitOfTheFirstKindAndHitsComeInOrder(t *testing.T) {
	terms := ScanTerms{Workspaces: []string{"shared", "beta"}, ThreadNames: []string{"shared"}, Identity: []string{"alice"}}
	hits, _ := Scan("beta and alice and shared\nshared alice beta\n", terms, nil)
	assert.Equal(t, []string{
		"1:1 workspace: beta",
		"1:10 identity: alice",
		"1:20 thread: shared",
		"2:1 thread: shared",
		"2:8 identity: alice",
		"2:14 workspace: beta",
	}, fmtHits(hits))
}

func TestScan_AnEmptyTermMatchesNothing(t *testing.T) {
	terms := ScanTerms{Workspaces: []string{"", "  "}, Sessions: []string{""}, Identity: []string{""}, Thread: []string{""}, ThreadNames: []string{" "}}
	hits, _ := Scan("hello world\n\n", terms, nil)
	assert.Empty(t, hits)
}

func TestScan_ATermWithInvalidUTF8DoesNotPanic(t *testing.T) {
	terms := ScanTerms{Sessions: []string{"bad\xffname"}}
	var hits []ScanHit
	assert.NotPanics(t, func() { hits, _ = Scan("badname bad\xffname\n", terms, nil) })
	assert.Equal(t, []string{"1:1 session: badname"}, fmtHits(hits), "the term is repaired, not dropped")
}

func TestScan_PrivatePrefixesAreHitsInEverySpellingGiven(t *testing.T) {
	terms := ScanTerms{Private: []string{"/tmp/ws-link", "/tmp/ws-real/", "/home/alice", "", "/", "relative/dir"}}
	text := "see /tmp/ws-link/core/x.go for it\n" +
		"and (/tmp/ws-real/sub) too\n" +
		"at /tmp/ws-link.\n" +
		"/tmp/ws-link2/x and /tmp/other/x\n" +
		"$HOME is /home/alice, ok\n" +
		"file:///tmp/ws-link/x\n" +
		"a / b relative/dir/x\n"
	hits, _ := Scan(text, terms, nil)
	assert.Equal(t, []string{
		"1:5 path: /tmp/ws-link/core/x.go",
		"2:6 path: /tmp/ws-real/sub",
		"3:4 path: /tmp/ws-link",
		"5:10 path: /home/alice",
		"6:8 path: /tmp/ws-link/x",
	}, fmtHits(hits))
}

func TestScan_AnyOtherAbsolutePathIsAHit(t *testing.T) {
	hits, _ := Scan("run /srv/data/x now\n(`/home/bob/y`)\nroot=/var/lib/thing\nkey:/opt/app/conf\n(/home/bob/z)\n", ScanTerms{}, nil)
	assert.Equal(t, []string{
		"1:5 path: /srv/data/x",
		"2:3 path: /home/bob/y",
		"3:6 path: /var/lib/thing",
		"4:5 path: /opt/app/conf",
		"5:2 path: /home/bob/z",
	}, fmtHits(hits))

	// A path is a hit unless the character before it shows it is part of a
	// longer token: markdown marks, a dash, an at sign, a backslash and a
	// file URL's empty authority all leave it a path.
	for name, text := range map[string]string{
		"after emphasis stars": "**/srv/data/x**",
		"after an em dash":     "\u2014/srv/data/x",
		"after an at sign":     "@/srv/data/x",
		"after a backslash":    `\/srv/data/x`,
		"after a bang":         "!/srv/data/x",
		"after a hash":         "#/srv/data/x",
		"after an underscore":  "_/srv/data/x",
		"after a dash":         "--root-/srv/data/x",
		"in a file URL":        "file:///srv/data/x",
	} {
		hits, _ := Scan(text, ScanTerms{}, nil)
		if assert.Len(t, hits, 1, name) {
			assert.Equal(t, ScanPath, hits[0].Kind, name)
			assert.Equal(t, "/srv/data/x", hits[0].Match, name)
		}
	}

	for name, text := range map[string]string{
		"/tmp":                "/tmp/a/b",
		"/dev":                "/dev/null/x",
		"/proc":               "/proc/1/status",
		"/usr":                "/usr/local/bin/x",
		"/etc":                "/etc/hosts",
		"/bin":                "/bin/sh",
		"one component":       "see /only here",
		"a URL":               "https://github.com/aidan-bailey/loom/issues/57",
		"another scheme":      "ssh://host/x/y",
		"a placeholder path":  "<repo>/core/x.go",
		"a relative path":     "core/x.go",
		"and/or":              "and/or",
		"dot-relative":        "./a/b and ../a/b",
		"a long relative":     "a/b/c",
		"a command":           "go test ./...",
		"a comment marker":    "// a comment",
		"a doc comment":       "/// docs",
		"a URL with a host":   "file://host/srv/data/x",
		"a URL after a word":  "see https://host/srv/data/x",
		"a path after a word": "x/srv/data/x",
		"an angle-bracketed":  "<repo>/srv/data/x",
		"a bare slash":        "a / b",
	} {
		hits, _ := Scan(text, ScanTerms{}, nil)
		assert.Empty(t, hits, name)
	}
}

func TestScan_HomeShorthandPathsAreHitsOutsideLoomAndClaudeDirs(t *testing.T) {
	hits, _ := Scan("cat ~/src/x\n~/.config/y\n~/.loomx/y\n~/.claudeish\n", ScanTerms{}, nil)
	assert.Equal(t, []string{
		"1:5 path: ~/src/x",
		"2:1 path: ~/.config/y",
		"3:1 path: ~/.loomx/y",
		"4:1 path: ~/.claudeish",
	}, fmtHits(hits))

	for name, text := range map[string]string{
		"loom state":      "~/.loom/state.json",
		"claude settings": "~/.claude/settings.json",
		"the loom dir":    "~/.loom",
		"the claude dir":  "~/.claude",
		"the placeholder": "run it in ~/ and see",
		"inside a word":   "x~/y",
	} {
		hits, _ := Scan(text, ScanTerms{}, nil)
		assert.Empty(t, hits, name)
	}
}

func TestScan_APathThatAThreadNamesIsAThreadHit(t *testing.T) {
	terms := ScanTerms{Thread: []string{"/srv/data/transcript.jsonl"}, Private: []string{"/srv/data"}}
	hits, _ := Scan("at /srv/data/transcript.jsonl and /srv/data/other\n", terms, nil)
	assert.Equal(t, []string{
		"1:4 thread: /srv/data/transcript.jsonl",
		"1:35 path: /srv/data/other",
	}, fmtHits(hits))
}
