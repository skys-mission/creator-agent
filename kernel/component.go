package kernel

import (
	"context"
	"sync"
)

// Component is a named application module with an explicit dependency list and
// a start/stop lifecycle. Name and Deps are snapshotted at registration and must
// be pure (identical results on every call).
//
// Start runs after all declared dependencies have started (G2). Stop is invoked
// only if Start returned nil (G4/G5) and receives a context bounded by
// App.StopTimeout; it must honor ctx cancellation.
type Component interface {
	Name() string
	Deps() []string
	Start(s *Scope) error
	Stop(ctx context.Context) error
}

// Funcs adapts plain functions to Component. OnStart and OnStop may be nil
// (treated as success / nothing to do).
type Funcs struct {
	CompName string
	CompDeps []string
	OnStart  func(s *Scope) error
	OnStop   func(ctx context.Context) error
}

// Name implements Component.
func (f Funcs) Name() string { return f.CompName }

// Deps implements Component.
func (f Funcs) Deps() []string { return f.CompDeps }

// Start implements Component.
func (f Funcs) Start(s *Scope) error {
	if f.OnStart == nil {
		return nil
	}
	return f.OnStart(s)
}

// Stop implements Component.
func (f Funcs) Stop(ctx context.Context) error {
	if f.OnStop == nil {
		return nil
	}
	return f.OnStop(ctx)
}

// Scope is a component's private handle for the duration of its lifetime. It
// carries the lifecycle context, access to the app registry, and the disposer
// list (the analog of cordis's ctx.effect). A Scope is not safe for concurrent
// use except Defer, which is.
type Scope struct {
	app *App
	ctx context.Context

	mu     sync.Mutex
	defers []func(context.Context)
	closed bool
}

// Context returns the component's lifecycle context: derived from the context
// passed to App.Start and canceled when shutdown begins (Shutdown or Stop), so
// long-running components can unwind cooperatively.
func (s *Scope) Context() context.Context { return s.ctx }

// App returns the owning app.
func (s *Scope) App() *App { return s.app }

// Component looks up a started component by name (untyped convenience wrapper
// over App.Component).
func (s *Scope) Component(name string) (Component, bool) { return s.app.Component(name) }

// Defer registers a disposer that releases a resource acquired during Start.
// Disposers run exactly once in LIFO order when this component's start attempt
// ends (G6). Registering after that point runs fn immediately (fail-closed:
// the resource is never leaked). fn must be non-blocking and must not panic;
// if it does, the panic is contained and reported through teardown errors.
func (s *Scope) Defer(fn func(context.Context)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = callDefer(fn, s.ctx)
		return
	}
	s.defers = append(s.defers, fn)
	s.mu.Unlock()
}

// runDefers executes the registered disposers in LIFO order exactly once and
// returns joined panic-as-error reports. Subsequent calls are no-ops.
func (s *Scope) runDefers(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	defers := s.defers
	s.defers = nil
	s.mu.Unlock()
	var errs []error
	for i := len(defers) - 1; i >= 0; i-- {
		if err := callDefer(defers[i], ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return joinAll(errs)
}

// callDefer runs one disposer, converting panics into errors.
func callDefer(fn func(context.Context), ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError("disposer", r)
		}
	}()
	fn(ctx)
	return nil
}
