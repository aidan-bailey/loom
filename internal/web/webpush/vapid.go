package webpush

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/core"
)

const (
	// vapidLifetime is how long a token is valid. RFC 8292 caps it at 24
	// hours; half of that leaves room for a push service's clock to differ.
	vapidLifetime = 12 * time.Hour
	// jwtHeader is RFC 8292 section 2's JOSE header: ES256 is ECDSA on P-256
	// with SHA-256.
	jwtHeader = `{"typ":"JWT","alg":"ES256"}`
)

// vapidClaims are the JWT's claims, in the order RFC 8292's example has them.
type vapidClaims struct {
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Sub string `json:"sub"`
}

// VAPIDAuth returns the Authorization header value for a push to endpoint:
// "vapid t=<JWT>, k=<public key>" (RFC 8292 section 3). The JWT is ES256 over
// the claims aud (the endpoint's origin, so one token serves every push to
// that service), exp (now plus 12 hours) and sub (subject, an https: or
// mailto: URI the push service can contact). key.Public must be key.Private's
// own point: a mismatch is refused, since the service would reject the token.
func VAPIDAuth(key core.VAPIDKey, endpoint, subject string, now time.Time) (string, error) {
	if !strings.HasPrefix(subject, "https://") && !strings.HasPrefix(subject, "mailto:") {
		return "", fmt.Errorf("webpush: VAPID subject %q is not an https: or mailto: URI", subject)
	}
	aud, err := audience(endpoint)
	if err != nil {
		return "", err
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), key.Private)
	if err != nil {
		return "", fmt.Errorf("webpush: VAPID private key: %w", err)
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return "", fmt.Errorf("webpush: VAPID public key: %w", err)
	}
	if !bytes.Equal(pub, key.Public) {
		return "", errors.New("webpush: the VAPID public key is not the private key's")
	}

	input, err := vapidSigningInput(aud, subject, now.Add(vapidLifetime).Unix())
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(input))
	der, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		return "", fmt.Errorf("webpush: signing: %w", err)
	}
	// SignASN1 answers a DER SEQUENCE of r and s; a JWS wants both as
	// 32-byte big-endian integers, one after the other.
	var rs struct{ R, S *big.Int }
	if rest, err := asn1.Unmarshal(der, &rs); err != nil || len(rest) != 0 {
		return "", errors.New("webpush: signing: malformed ECDSA signature")
	}
	sig := jwsSignature(rs.R, rs.S)

	return "vapid t=" + input + "." + base64.RawURLEncoding.EncodeToString(sig) +
		", k=" + base64.RawURLEncoding.EncodeToString(key.Public), nil
}

// jwsSignature is RFC 7518 section 3.4's ES256 signature: r then s, each a
// 32-byte big-endian integer. A leading zero byte stays; r.Bytes() would drop
// it and shorten the signature, which a push service rejects.
func jwsSignature(r, s *big.Int) []byte {
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig
}

// vapidSigningInput is the JWT's "header.claims" text, which the signature covers.
func vapidSigningInput(aud, sub string, exp int64) (string, error) {
	claims, err := json.Marshal(vapidClaims{Aud: aud, Exp: exp, Sub: sub})
	if err != nil {
		return "", fmt.Errorf("webpush: claims: %w", err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(jwtHeader)) + "." + enc.EncodeToString(claims), nil
}

// audience is the endpoint's origin as RFC 6454 writes it, which is what a push
// service compares: "https://", the lower-case host (an IPv6 literal in
// brackets) and the port unless it is https's default, 443; no path. An
// endpoint with no host, or a scheme other than https, is no push resource:
// a VAPID token over http would cross the network in the clear.
func audience(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("webpush: endpoint: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" || host == "" {
		return "", errors.New("webpush: the endpoint is not an https URL with a host")
	}
	if port := u.Port(); port != "" && port != "443" {
		return "https://" + net.JoinHostPort(host, port), nil
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, nil
}
