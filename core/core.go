// Package core exposes the single functionality surface of the agent runtime.
// Every channel (in-process direct calls, the stdio protocol, the gRPC network
// face) is a consumer of these methods and must not re-implement logic.
//
// P0 carries only the smoke surface (Status); the agent loop, sessions, tools
// and plugins land here in later phases (docs/architecture.md §4-6).
package core

import (
	"context"
	"time"
)

// Core is the runtime kernel. Constructed once per process and shared by all
// channels.
type Core struct {
	version string
	started time.Time
}

// New returns a Core reporting the given version string.
func New(version string) *Core {
	return &Core{version: version, started: time.Now()}
}

// Status is the P0 smoke surface: it proves the direct-call path works without
// any network involvement.
type Status struct {
	Version string
	Uptime  time.Duration
}

// Status reports the runtime status. It honors ctx cancellation so callers on
// any channel can abort.
func (c *Core) Status(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	return Status{
		Version: c.version,
		Uptime:  time.Since(c.started).Round(time.Second),
	}, nil
}
