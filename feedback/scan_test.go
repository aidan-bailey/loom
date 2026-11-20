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
