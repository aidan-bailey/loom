package web

import (
	"reflect"
	"slices"
)

//go:generate go run ./internal/gen/cmd

// ContractRoute is one route of the table as the generator reads it: what
// routes.go declares for it, and the types it decodes and answers with.
type ContractRoute struct {
	Method string
	Path   string
	Open   bool
	Client string
	OK     int
	Doc    string
	Query  reflect.Type
	Body   reflect.Type
	Resp   reflect.Type
	Page   bool
}

// ContractEvent is one stream event: its SSE name and its payload's type.
type ContractEvent struct {
	Name string
	Data reflect.Type
}

// ContractUnion is a TS string union: its name and its values, in order.
type ContractUnion struct {
	Name   string
	Values []string
}

// ContractSpec is everything the generator reads: the route table, the
// stream's events and the typed string types that become unions.
type ContractSpec struct {
	Routes []ContractRoute
	Events []ContractEvent
	Unions map[reflect.Type]ContractUnion
}

// Contract is the route table, the stream's events table and the unions as
// one value, built from a Layer with no client: the handlers in the table
// close over the layer and run only when a request reaches them, so the
// generator needs no model. internal/web/internal/gen turns it into
// ui/src/api/gen.ts, docs/specs/web-api.md and api.fields.
func Contract() ContractSpec {
	l := &Layer{}
	var c ContractSpec
	for _, r := range l.routes() {
		c.Routes = append(c.Routes, ContractRoute{
			Method: r.spec.method,
			Path:   r.spec.path,
			Open:   r.spec.open,
			Client: r.spec.client,
			OK:     r.spec.ok,
			Doc:    r.spec.doc,
			Query:  r.query,
			Body:   r.body,
			Resp:   r.resp,
			Page:   r.page,
		})
	}
	for _, e := range streamEvents() {
		c.Events = append(c.Events, ContractEvent{Name: e.name, Data: e.data})
	}
	known := unions()
	c.Unions = make(map[reflect.Type]ContractUnion, len(known))
	for t, u := range known {
		c.Unions[t] = ContractUnion{Name: u.name, Values: slices.Clone(u.values)}
	}
	return c
}
