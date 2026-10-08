package core

import (
	"encoding/json"

	"github.com/aidan-bailey/loom/session/github"
)

// The events whose fields hold an error encode it as a WireError, which
// keeps the message and the sentinel identities clients test (errors.Is).
// Every other event encodes as plain JSON.

type noticeJSON struct {
	Err  *WireError `json:",omitempty"`
	Info string     `json:",omitempty"`
	Req  ReqID      `json:",omitempty"`
}

// MarshalJSON encodes n with its error as a WireError.
func (n Notice) MarshalJSON() ([]byte, error) {
	return json.Marshal(noticeJSON{Err: ToWire(n.Err), Info: n.Info, Req: n.Req})
}

// UnmarshalJSON decodes a Notice MarshalJSON encoded.
func (n *Notice) UnmarshalJSON(b []byte) error {
	var j noticeJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*n = Notice{Err: FromWire(j.Err), Info: j.Info, Req: j.Req}
	return nil
}

type replyJSON struct {
	Req    ReqID
	ID     InstanceID
	Err    *WireError `json:",omitempty"`
	Notice *WireError `json:",omitempty"`
	Issue  github.Issue
}

// MarshalJSON encodes r with its errors as WireErrors.
func (r Reply) MarshalJSON() ([]byte, error) {
	return json.Marshal(replyJSON{Req: r.Req, ID: r.ID, Err: ToWire(r.Err), Notice: ToWire(r.Notice), Issue: r.Issue})
}

// UnmarshalJSON decodes a Reply MarshalJSON encoded.
func (r *Reply) UnmarshalJSON(b []byte) error {
	var j replyJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*r = Reply{Req: j.Req, ID: j.ID, Err: FromWire(j.Err), Notice: FromWire(j.Notice), Issue: j.Issue}
	return nil
}
