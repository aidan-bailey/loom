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
	sig := make([]byte, 64)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:])

	return "vapid t=" + input + "." + base64.RawURLEncoding.EncodeToString(sig) +
		", k=" + base64.RawURLEncoding.EncodeToString(key.Public), nil
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

// audience is the endpoint's origin: scheme, host and any port, no path. An
// endpoint with no host, or a scheme that is not http or https, is no push
// resource.
func audience(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("webpush: endpoint: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("webpush: the endpoint is not an http(s) URL with a host")
	}
	return u.Scheme + "://" + u.Host, nil
}
