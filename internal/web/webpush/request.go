package webpush

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/core"
)

// maxTopic is RFC 8030 section 5.4's cap on a Topic header's length.
const maxTopic = 32

// Message is one push: its plaintext payload and the delivery headers of RFC 8030.
type Message struct {
	Payload []byte
	// TTL is how long the push service keeps the message for an offline
	// device, sent in whole seconds.
	TTL time.Duration
	// Urgency is "very-low", "low", "normal" or "high" (RFC 8030 section 5.3);
	// empty sends no Urgency header, which means normal.
	Urgency string
	// Topic replaces any waiting message with the same topic (section 5.4):
	// up to 32 characters of the URL-safe base64 alphabet, of a length
	// unpadded base64url can have (never 1 mod 4, which Apple refuses);
	// empty sends none.
	Topic string
}

// NewRequest builds the POST that delivers msg to sub: the payload encrypted
// by Encrypt, the VAPID Authorization from VAPIDAuth, and the TTL, Urgency and
// Topic headers. It sends nothing; the caller sets the timeout, through ctx or
// its client, and reads the push service's status.
func NewRequest(ctx context.Context, sub Subscription, key core.VAPIDKey, subject string, msg Message, now time.Time) (*http.Request, error) {
	switch msg.Urgency {
	case "", "very-low", "low", "normal", "high":
	default:
		return nil, fmt.Errorf("webpush: unknown urgency %q", msg.Urgency)
	}
	if !validTopic(msg.Topic) {
		return nil, fmt.Errorf("webpush: topic %q is over %d characters, outside the URL-safe base64 alphabet, or of a length that is 1 mod 4", msg.Topic, maxTopic)
	}
	if msg.TTL < 0 {
		return nil, fmt.Errorf("webpush: negative TTL %v", msg.TTL)
	}
	body, err := Encrypt(sub, msg.Payload)
	if err != nil {
		return nil, err
	}
	auth, err := VAPIDAuth(key, sub.Endpoint, subject, now)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("webpush: request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.FormatInt(int64(msg.TTL/time.Second), 10))
	if msg.Urgency != "" {
		req.Header.Set("Urgency", msg.Urgency)
	}
	if msg.Topic != "" {
		req.Header.Set("Topic", msg.Topic)
	}
	return req, nil
}

// validTopic accepts an empty topic, or up to 32 characters of A-Z, a-z, 0-9,
// "-" and "_" whose length is not 1 mod 4: unpadded base64url never has such
// a length, and Apple answers one with 400 BadWebPushTopic although RFC 8030
// allows it (the spike, 2026-10-22).
func validTopic(topic string) bool {
	if len(topic) > maxTopic || len(topic)%4 == 1 {
		return false
	}
	for _, c := range topic {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
