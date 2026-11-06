package sched

import (
	"context"
	"fmt"
	"math"

	sddbus "github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
)

// systemdConfiner is the Confiner of the user's systemd, on the session bus.
type systemdConfiner struct {
	conn *sddbus.Conn
	stop context.CancelFunc // ends the context the connection lives by
}

// NewConfiner connects to the user's service manager. ctx bounds the
// connecting only: godbus closes a connection once the context it was dialed
// with ends, so the connection is dialed with a context of its own, which
// Close ends.
func NewConfiner(ctx context.Context) (Confiner, error) {
	c, err := dialConfiner(ctx, sddbus.NewUserConnectionContext)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// dialConfiner is NewConfiner with its dial, which a test replaces. A dial
// that fails or outlasts ctx ends the connection's context, which closes a
// connection the dial makes late.
func dialConfiner(ctx context.Context, dial func(context.Context) (*sddbus.Conn, error)) (*systemdConfiner, error) {
	life, stop := context.WithCancel(context.WithoutCancel(ctx))
	type dialed struct {
		conn *sddbus.Conn
		err  error
	}
	got := make(chan dialed, 1)
	go func() {
		conn, err := dial(life)
		got <- dialed{conn: conn, err: err}
	}()
	select {
	case d := <-got:
		if d.err != nil {
			stop()
			return nil, fmt.Errorf("connect to the user's systemd: %w", d.err)
		}
		return &systemdConfiner{conn: d.conn, stop: stop}, nil
	case <-ctx.Done():
		stop()
		return nil, fmt.Errorf("connect to the user's systemd: %w", ctx.Err())
	}
}

// prop is a unit property of any D-Bus value.
func prop(name string, v any) sddbus.Property {
	return sddbus.Property{Name: name, Value: godbus.MakeVariant(v)}
}

// limit is bytes as a systemd memory limit: 0 is infinity.
func limit(bytes uint64) uint64 {
	if bytes == 0 {
		return math.MaxUint64
	}
	return bytes
}

// start creates unit as a transient unit with props and waits for its start
// job.
func (c *systemdConfiner) start(ctx context.Context, unit string, props []sddbus.Property) error {
	done := make(chan string, 1)
	if _, err := c.conn.StartTransientUnitContext(ctx, unit, "fail", props, done); err != nil {
		return fmt.Errorf("create %s: %w", unit, err)
	}
	select {
	case result := <-done:
		if result != "done" {
			return fmt.Errorf("create %s: its start job ended %q", unit, result)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("create %s: %w", unit, ctx.Err())
	}
}

func (c *systemdConfiner) JoinScope(ctx context.Context, s ScopeSpec, pid int) error {
	return c.start(ctx, s.Unit, []sddbus.Property{
		sddbus.PropDescription("loom job"),
		sddbus.PropSlice(s.Slice),
		sddbus.PropPids(uint32(pid)),
		prop("CPUWeight", s.CPUWeight),
		prop("CPUAccounting", true),
		prop("MemoryAccounting", true),
		prop("OOMPolicy", "kill"),
		prop("CollectMode", "inactive-or-failed"),
	})
}

func (c *systemdConfiner) EnsureSlice(ctx context.Context, name string, p SliceProps) error {
	props := []sddbus.Property{prop("CPUWeight", p.CPUWeight), prop("MemoryMax", limit(p.MemoryMaxBytes))}
	setErr := c.conn.SetUnitPropertiesContext(ctx, name, true, props...)
	if setErr == nil {
		return nil
	}
	if err := c.start(ctx, name, append(props, sddbus.PropDescription("loom jobs"))); err != nil {
		return fmt.Errorf("set %s: %v; %w", name, setErr, err)
	}
	return nil
}

func (c *systemdConfiner) SetMemoryHigh(ctx context.Context, unit string, bytes uint64) error {
	if err := c.conn.SetUnitPropertiesContext(ctx, unit, true, prop("MemoryHigh", limit(bytes))); err != nil {
		return fmt.Errorf("set %s's MemoryHigh: %w", unit, err)
	}
	return nil
}

func (c *systemdConfiner) Freeze(ctx context.Context, unit string) error {
	if err := c.conn.FreezeUnit(ctx, unit); err != nil {
		return fmt.Errorf("freeze %s: %w", unit, err)
	}
	return nil
}

func (c *systemdConfiner) Thaw(ctx context.Context, unit string) error {
	if err := c.conn.ThawUnit(ctx, unit); err != nil {
		return fmt.Errorf("thaw %s: %w", unit, err)
	}
	return nil
}

func (c *systemdConfiner) Close() {
	c.conn.Close()
	c.stop()
}
