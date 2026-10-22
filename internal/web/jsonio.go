package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aidan-bailey/loom/log"
)

// writeJSON answers status with v as a JSON body. A value that will not
// encode is logged and answered as a 500 with a fixed body: the encoding
// happens before the header, so a handler's bug never sends half a response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.For("web").Error("web.encode_failed", "err", err)
		status, body = http.StatusInternalServerError, []byte(`{"error":"the answer could not be encoded"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// writeError answers status with {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readJSON decodes r's body, which must hold exactly one JSON value, into v.
// An empty body, a field v does not have, a second value and a body past the
// cap (guard's http.MaxBytesReader) are errors whose text can go back to the
// client in a 400.
func readJSON(r *http.Request, v any) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return fmt.Errorf("the body is over %d bytes", tooBig.Limit)
		}
		return fmt.Errorf("the body could not be read: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("the body is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the body is not the JSON this route takes: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the body holds more than one JSON value")
	}
	return nil
}
