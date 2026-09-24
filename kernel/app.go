package kernel

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
)

// Lifecycle and registry errors. All are testable with errors.Is.
var (
	// ErrBadState reports an operation illegal in the current lifecycle state (G7).
	ErrBadState = errors.New("kernel: illegal state transition")
	// ErrDuplicate reports a second registration under an existing name (G1).
	ErrDuplicate = errors.New("kernel: duplicate component name")
	// ErrEmptyName reports a component with a blank name (G1).
	ErrEmptyName = errors.New("kernel: empty component name")
)

// State is the app lifecycle state. See package doc G7 for legal transitions.
type State int32

// Lifecycle states.
const (
	StateNew      State = iota // accepting registrations
	StateStarting              // resolving dependencies and starting components
	StateRunning               // all components started
	StateStopping              // tearing down
	StateStopped               // terminal
)

// String implements fmt.Stringer (used in errors).
func (s State) String() string {
	switch s {
	case StateNew:
		return "new"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	default:
		return fmt.Sprintf("state(%d)", int32(s))
	}
}

// entry is the registration-time snapshot of a component (G3: Name and Deps are
// read once, so later mutation cannot reorder startup).
type entry struct {
	comp Component
	name string
	deps []string
}

// startedEntry pairs a successfully started component with the Scope holding
// its disposers.
type startedEntry struct {
	entry entry
	scope *Scope
}

// App owns the component registry and the global lifecycle. Registration (Add)
// and lifecycle drivers (Start/Stop/Run) are serialized by the owner goroutine;
// Component, Get, State, Done and Shutdown are safe from any goroutine.
type App struct {
	// StopTimeout bounds each individual component's Stop during teardown; 0
	// disables the bound and relies entirely on components honoring ctx.
	// Set before Start. Default 15s.
	StopTimeout time.Duration

	mu         sync.RWMutex
	state      State
	entries    []entry
	reg        map[string]struct{}
	order      []string
	started    []startedEntry
	live       map[string]Component
	lifeCancel context.CancelFunc
	stopErr    error

	shutdown chan struct{}
	shutOnce sync.Once
}

// New returns an empty app in state New.
func New() *App {
	return &App{
		StopTimeout: 15 * time.Second,
		reg:         map[string]struct{}{},
		live:        map[string]Component{},
		shutdown:    make(chan struct{}),
	}
}

// Add registers a component (G1). Only legal in state New; after Start the
// registry is frozen (G7).
func (a *App) Add(c Component) error {
	if c == nil {
		return errors.New("kernel: nil component")
	}
	name := strings.TrimSpace(c.Name())
	if name == "" {
		return fmt.Errorf("%w: %T", ErrEmptyName, c)
	}
	var deps []string
	if ds := c.Deps(); len(ds) > 0 {
		deps = slices.Clone(ds)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != StateNew {
		return fmt.Errorf("%w: register %q in state %s", ErrBadState, name, a.state)
	}
	if _, dup := a.reg[name]; dup {
		return fmt.Errorf("%w: %q", ErrDuplicate, name)
	}
	a.reg[name] = struct{}{}
	a.entries = append(a.entries, entry{comp: c, name: name, deps: deps})
	return nil
}

// State returns the current lifecycle state.
func (a *App) State() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// Order returns a copy of the resolved start order (valid after a successful
// Start; nil before). Intended for diagnostics and tests.
func (a *App) Order() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return slices.Clone(a.order)
}

// Component returns the started component registered under name (G7: only
// successfully started components are visible). The name is trimmed.
func (a *App) Component(name string) (Component, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.live[strings.TrimSpace(name)]
	return c, ok
}

// Get returns the started component registered under name, asserted to T.
// Reports false when the component is unknown, not yet started, or does not
// implement T.
func Get[T any](a *App, name string) (T, bool) {
	var zero T
	c, ok := a.Component(name)
	if !ok {
		return zero, false
	}
	v, ok := c.(T)
	if !ok {
		return zero, false
	}
	return v, true
}

// Done is closed when shutdown begins (Shutdown or Stop). It is valid from New
// onward and never nil, so callers may select on it before Start.
func (a *App) Done() <-chan struct{} { return a.shutdown }

// Shutdown signals global shutdown: it cancels the lifecycle context so
// components can unwind, and closes Done. It does not itself stop components —
// App.Run or App.Stop does the teardown. Safe from any goroutine; idempotent.
func (a *App) Shutdown() {
	a.shutOnce.Do(func() { close(a.shutdown) })
	a.mu.RLock()
	cancel := a.lifeCancel
	a.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// Start resolves the dependency graph and starts every component in topological
// order (G2/G3). On the first failure it rolls back (G4) and returns a joined
// error; on success the app enters Running.
//
// Start and Stop must not be called concurrently with each other.
func (a *App) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if a.state != StateNew {
		st := a.state
		a.mu.Unlock()
		return fmt.Errorf("%w: Start in state %s", ErrBadState, st)
	}
	a.state = StateStarting
	entries := slices.Clone(a.entries)
	a.mu.Unlock()

	names := make([]string, len(entries))
	byName := make(map[string]entry, len(entries))
	depMap := make(map[string][]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
		byName[e.name] = e
		depMap[e.name] = e.deps
	}
	order, err := resolve(names, depMap)
	if err != nil {
		return a.failStart(err, nil, nil)
	}

	lifeCtx, lifeCancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.lifeCancel = lifeCancel
	a.order = order
	a.mu.Unlock()

	started := make([]startedEntry, 0, len(order))
	for _, name := range order {
		if err := lifeCtx.Err(); err != nil {
			return a.failStart(fmt.Errorf("kernel: startup aborted before %q: %w", name, err), started, nil)
		}
		e := byName[name]
		sc := &Scope{app: a, ctx: lifeCtx}
		if err := callStart(e, sc); err != nil {
			return a.failStart(fmt.Errorf("kernel: start %q: %w", name, err), started, sc)
		}
		se := startedEntry{entry: e, scope: sc}
		started = append(started, se)
		a.mu.Lock()
		a.started = append(a.started, se)
		a.live[name] = e.comp
		a.mu.Unlock()
	}

	a.mu.Lock()
	a.state = StateRunning
	a.mu.Unlock()
	return nil
}

// Stop tears down started components in exact reverse start order (G5) and
// enters Stopped. Idempotent: once stopped, later calls return the first
// teardown result unchanged and never touch components again. A Stop in state
// New transitions straight to Stopped. Concurrent Stop calls are rejected with
// ErrBadState (Start/Stop are owner-serialized).
//
// The passed ctx bounds the whole teardown (each component additionally gets
// App.StopTimeout). Callers triggering shutdown from a canceled context should
// pass context.Background() so resource release is not starved.
func (a *App) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	switch a.state {
	case StateStopped:
		err := a.stopErr
		a.mu.Unlock()
		return err
	case StateNew:
		a.state = StateStopped
		a.mu.Unlock()
		return nil
	case StateStarting:
		a.mu.Unlock()
		return fmt.Errorf("%w: Stop during Start", ErrBadState)
	case StateStopping:
		a.mu.Unlock()
		return fmt.Errorf("%w: Stop already in progress", ErrBadState)
	}
	a.state = StateStopping
	started := slices.Clone(a.started)
	a.mu.Unlock()

	a.Shutdown() // shutdown begins: cancel lifecycle ctx, close Done

	err := a.teardown(ctx, started)

	a.mu.Lock()
	a.state = StateStopped
	a.stopErr = err
	a.started = nil
	a.live = map[string]Component{}
	a.order = nil
	a.mu.Unlock()
	return err
}

// Run starts all components, blocks until ctx is done or Shutdown is called,
// then tears everything down. Start failures are returned after rollback; the
// return value of a graceful shutdown is the teardown error (nil when clean).
func (a *App) Run(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-a.Done():
	}
	return a.Stop(context.Background())
}

// failStart records a start failure (G4): runs the failed scope's disposers,
// rolls back all previously started components in reverse order, and parks the
// app in Stopped. prior components never include the failed one — a component
// whose Start failed gets disposers but never Stop.
func (a *App) failStart(cause error, prior []startedEntry, failed *Scope) error {
	errs := []error{cause}
	if failed != nil {
		if err := failed.runDefers(context.WithoutCancel(context.Background())); err != nil {
			errs = append(errs, fmt.Errorf("kernel: dispose on failed start: %w", err))
		}
	}
	rollbackErr := a.teardown(context.Background(), prior)
	if rollbackErr != nil {
		errs = append(errs, rollbackErr)
	}

	a.mu.Lock()
	a.state = StateStopped
	a.stopErr = rollbackErr
	a.started = nil
	a.live = map[string]Component{}
	a.order = nil
	cancel := a.lifeCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.Shutdown()
	return joinAll(errs)
}

// teardown stops the given components in reverse order (G5): each Stop runs
// under App.StopTimeout with panic containment, then the component's disposers
// run in LIFO order on a non-cancelable context (G6). Never aborts early.
func (a *App) teardown(ctx context.Context, started []startedEntry) error {
	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		se := started[i]
		cctx := ctx
		done := func() {}
		if a.StopTimeout > 0 {
			cctx, done = context.WithTimeout(ctx, a.StopTimeout)
		}
		if err := callStop(se.entry, cctx); err != nil {
			errs = append(errs, fmt.Errorf("kernel: stop %q: %w", se.entry.name, err))
		}
		done()
		if err := se.scope.runDefers(context.WithoutCancel(ctx)); err != nil {
			errs = append(errs, fmt.Errorf("kernel: dispose %q: %w", se.entry.name, err))
		}
	}
	return joinAll(errs)
}

// callStart invokes Component.Start with panic containment (G4).
func callStart(e entry, sc *Scope) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError("start "+e.name, r)
		}
	}()
	return e.comp.Start(sc)
}

// callStop invokes Component.Stop with panic containment (G5).
func callStop(e entry, ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError("stop "+e.name, r)
		}
	}()
	return e.comp.Stop(ctx)
}

// panicError converts a recovered panic into an error carrying its stack.
func panicError(op string, r any) error {
	return fmt.Errorf("kernel: %s panicked: %v\n%s", op, r, debug.Stack())
}

// joinAll is errors.Join with nil for an empty list.
func joinAll(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
