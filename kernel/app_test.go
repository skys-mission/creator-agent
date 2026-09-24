package kernel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- test doubles ----

type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

type testComp struct {
	name string
	deps []string
	rec  *recorder

	// start runs after logging, before the panic flag; it may register
	// disposers on the scope or fail the component.
	start func(s *Scope) error
	// stopFn runs after logging (and after the panic/block flags).
	stopFn func(ctx context.Context) error

	panicOnStart bool
	panicOnStop  bool
	blockStop    bool // Stop blocks until its ctx is canceled (cooperative)
}

func (c *testComp) Name() string   { return c.name }
func (c *testComp) Deps() []string { return c.deps }
func (c *testComp) Mark()          {} // marker for typed-lookup tests

func (c *testComp) Start(s *Scope) error {
	c.rec.add("start:" + c.name)
	if c.panicOnStart {
		panic("boom start " + c.name)
	}
	if c.start != nil {
		return c.start(s)
	}
	return nil
}

func (c *testComp) Stop(ctx context.Context) error {
	c.rec.add("stop:" + c.name)
	if c.panicOnStop {
		panic("boom stop " + c.name)
	}
	if c.blockStop {
		<-ctx.Done()
		c.rec.add("stop-cancel:" + c.name)
		return ctx.Err()
	}
	if c.stopFn != nil {
		return c.stopFn(ctx)
	}
	return nil
}

type marker interface{ Mark() }

var errBoom = errors.New("boom")

func mustAdd(t *testing.T, a *App, cs ...Component) {
	t.Helper()
	for _, c := range cs {
		if err := a.Add(c); err != nil {
			t.Fatalf("Add(%s) err = %v", c.Name(), err)
		}
	}
}

// ---- registration ----

func TestAddValidation(t *testing.T) {
	a := New()
	if err := a.Add(nil); err == nil {
		t.Fatal("Add(nil) succeeded")
	}
	if err := a.Add(&testComp{name: "   ", rec: &recorder{}}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("Add(blank) err = %v, want ErrEmptyName", err)
	}
	mustAdd(t, a, &testComp{name: "a", rec: &recorder{}})
	if err := a.Add(&testComp{name: "a", rec: &recorder{}}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Add(dup) err = %v, want ErrDuplicate", err)
	}
}

// ---- ordering and teardown (G2/G3/G5) ----

func TestStartOrderAndStopReverse(t *testing.T) {
	rec := &recorder{}
	a := New()
	// Chain: d depends on c depends on b depends on a. Registration scrambled.
	mustAdd(t, a,
		&testComp{name: "c", deps: []string{"b"}, rec: rec},
		&testComp{name: "a", rec: rec},
		&testComp{name: "d", deps: []string{"c"}, rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec},
	)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	if got, want := rec.snapshot(), []string{"start:a", "start:b", "start:c", "start:d"}; !slices.Equal(got, want) {
		t.Fatalf("start log = %v, want %v", got, want)
	}
	if got, want := a.Order(), []string{"a", "b", "c", "d"}; !slices.Equal(got, want) {
		t.Fatalf("Order() = %v, want %v", got, want)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	want := []string{"start:a", "start:b", "start:c", "start:d", "stop:d", "stop:c", "stop:b", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

func TestStartOrderDeterministic(t *testing.T) {
	build := func() *App {
		rec := &recorder{}
		a := New()
		mustAdd(t, a,
			&testComp{name: "p", deps: []string{"q"}, rec: rec},
			&testComp{name: "q", rec: rec},
			&testComp{name: "r", deps: []string{"q", "p"}, rec: rec},
			&testComp{name: "s", rec: rec},
		)
		if err := a.Start(context.Background()); err != nil {
			t.Fatalf("Start err = %v", err)
		}
		return a
	}
	if got, want := build().Order(), build().Order(); !slices.Equal(got, want) {
		t.Fatalf("order not deterministic:\n%v\n%v", got, want)
	}
}

// ---- failure atomicity (G4) ----

func TestStartMissingDepFailsBeforeAnyStart(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a, &testComp{name: "a", deps: []string{"ghost"}, rec: rec})
	if err := a.Start(context.Background()); !errors.Is(err, ErrMissingDep) {
		t.Fatalf("Start err = %v, want ErrMissingDep", err)
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("log = %v, want empty (nothing may start)", got)
	}
	if a.State() != StateStopped {
		t.Fatalf("state = %s, want stopped", a.State())
	}
}

func TestStartRollbackOnFailure(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a,
		&testComp{name: "a", rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec},
		&testComp{name: "c", deps: []string{"b"}, rec: rec,
			start: func(s *Scope) error {
				s.Defer(func(context.Context) { rec.add("dispose:c") })
				return errBoom
			}},
		&testComp{name: "d", deps: []string{"c"}, rec: rec},
		&testComp{name: "e", deps: []string{"d"}, rec: rec},
	)
	err := a.Start(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Start err = %v, want errBoom", err)
	}
	want := []string{"start:a", "start:b", "start:c", "dispose:c", "stop:b", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v (failed comp gets disposers, never Stop)", got, want)
	}
	if a.State() != StateStopped {
		t.Fatalf("state = %s, want stopped", a.State())
	}
	if _, ok := a.Component("a"); ok {
		t.Fatal("started map must be empty after rollback")
	}
}

func TestPanicInStartBecomesErrorAndRollsBack(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a,
		&testComp{name: "a", rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec, panicOnStart: true},
	)
	err := a.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("Start err = %v, want panic report", err)
	}
	want := []string{"start:a", "start:b", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

// ---- teardown robustness (G5/G6) ----

func TestPanicInStopDoesNotAbortTeardown(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a,
		&testComp{name: "a", rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec},
		&testComp{name: "c", deps: []string{"b"}, rec: rec, panicOnStop: true},
	)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	err := a.Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("Stop err = %v, want panic report", err)
	}
	want := []string{"start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v (teardown must continue)", got, want)
	}
}

func TestStopIdempotentKeepsResult(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a, &testComp{name: "a", rec: rec,
		stopFn: func(context.Context) error { return errBoom }})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	first := a.Stop(context.Background())
	if !errors.Is(first, errBoom) {
		t.Fatalf("first Stop err = %v, want errBoom", first)
	}
	second := a.Stop(context.Background())
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("second Stop err = %v, want same as first (%v)", second, first)
	}
	want := []string{"start:a", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v (Stop at most once)", got, want)
	}
}

func TestDeferLIFOExactlyOnce(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a, &testComp{name: "a", rec: rec,
		start: func(s *Scope) error {
			for _, n := range []string{"d1", "d2", "d3"} {
				s.Defer(func(context.Context) { rec.add("dispose:" + n) })
			}
			return nil
		}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	_ = a.Stop(context.Background())
	want := []string{"start:a", "stop:a", "dispose:d3", "dispose:d2", "dispose:d1"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

func TestDeferAfterTeardownRunsImmediately(t *testing.T) {
	rec := &recorder{}
	a := New()
	var scope *Scope
	mustAdd(t, a, &testComp{name: "a", rec: rec,
		start: func(s *Scope) error { scope = s; return nil }})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	scope.Defer(func(context.Context) { rec.add("dispose:late") })
	want := []string{"start:a", "stop:a", "dispose:late"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v (late disposer must run at once)", got, want)
	}
}

func TestStopTimeoutBounded(t *testing.T) {
	rec := &recorder{}
	a := New()
	a.StopTimeout = 50 * time.Millisecond
	mustAdd(t, a,
		&testComp{name: "a", rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec, blockStop: true},
	)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	begin := time.Now()
	err := a.Stop(context.Background())
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("Stop took %v, want bounded by StopTimeout", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop err = %v, want deadline report", err)
	}
	want := []string{"start:a", "start:b", "stop:b", "stop-cancel:b", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v (teardown continues past hung Stop)", got, want)
	}
}

// ---- global lifecycle (G7) ----

func TestStateMachine(t *testing.T) {
	a := New()
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start err = %v", err)
	}
	if a.State() != StateStopped {
		t.Fatalf("state = %s, want stopped", a.State())
	}

	b := New()
	mustAdd(t, b, &testComp{name: "a", rec: &recorder{}})
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	if err := b.Add(&testComp{name: "late", rec: &recorder{}}); !errors.Is(err, ErrBadState) {
		t.Fatalf("Add after Start err = %v, want ErrBadState", err)
	}
	if err := b.Start(context.Background()); !errors.Is(err, ErrBadState) {
		t.Fatalf("Start twice err = %v, want ErrBadState", err)
	}
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	if err := b.Start(context.Background()); !errors.Is(err, ErrBadState) {
		t.Fatalf("Start after Stop err = %v, want ErrBadState", err)
	}
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("Stop twice err = %v, want nil", err)
	}
}

func TestGetTypedLookupAndVisibility(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a,
		&testComp{name: "a", rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec,
			start: func(*Scope) error {
				if _, ok := Get[marker](a, "a"); !ok {
					t.Error("dependency 'a' not visible inside dependent's Start")
				}
				if _, ok := Get[marker](a, "b"); ok {
					t.Error("component must not be visible inside its own Start")
				}
				if _, ok := Get[marker](a, "ghost"); ok {
					t.Error("unknown component reported visible")
				}
				if _, ok := Get[interface{ Other() }](a, "a"); ok {
					t.Error("typed lookup must fail when the type does not match")
				}
				return nil
			}},
	)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	if _, ok := Get[marker](a, "a"); !ok {
		t.Fatal("started component not visible after Start")
	}
}

func TestShutdownUnblocksRunAndStops(t *testing.T) {
	rec := &recorder{}
	a := New()
	started := make(chan struct{})
	mustAdd(t, a, &testComp{name: "a", rec: rec,
		start: func(*Scope) error { close(started); return nil }})
	errCh := make(chan error, 1)
	go func() { errCh <- a.Run(context.Background()) }()
	<-started
	a.Shutdown()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after Shutdown")
	}
	want := []string{"start:a", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("Done must be closed after shutdown")
	}
}

func TestParentCancelStopsRun(t *testing.T) {
	rec := &recorder{}
	a := New()
	started := make(chan struct{})
	mustAdd(t, a, &testComp{name: "a", rec: rec,
		start: func(*Scope) error { close(started); return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- a.Run(ctx) }()
	<-started
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after parent cancel")
	}
	want := []string{"start:a", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

func TestShutdownDuringStartAbortsAndRollsBack(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a,
		&testComp{name: "a", rec: rec},
		&testComp{name: "b", deps: []string{"a"}, rec: rec,
			start: func(*Scope) error { a.Shutdown(); return nil }},
		&testComp{name: "c", deps: []string{"b"}, rec: rec},
	)
	err := a.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("Start err = %v, want startup-abort report", err)
	}
	want := []string{"start:a", "start:b", "stop:b", "stop:a"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	if a.State() != StateStopped {
		t.Fatalf("state = %s, want stopped", a.State())
	}
}

func TestFuncsAdapter(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a,
		Funcs{CompName: "noop"}, // nil OnStart/OnStop: success, nothing to do
		Funcs{
			CompName: "f",
			CompDeps: []string{"noop"},
			OnStart:  func(*Scope) error { rec.add("start:f"); return nil },
			OnStop:   func(context.Context) error { rec.add("stop:f"); return nil },
		},
	)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	want := []string{"start:f", "stop:f"}
	if got := rec.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

// TestConcurrentLookupsDuringStop exercises the read paths against teardown
// under the race detector (Component/Get/State/Done are goroutine-safe).
func TestConcurrentLookupsDuringStop(t *testing.T) {
	rec := &recorder{}
	a := New()
	mustAdd(t, a, &testComp{name: "a", rec: rec,
		stopFn: func(context.Context) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = a.Component("a")
				_, _ = Get[marker](a, "a")
				_ = a.State()
				_ = fmt.Sprint(a.State())
				select {
				case <-a.Done():
				default:
				}
			}
		}()
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	wg.Wait()
}
