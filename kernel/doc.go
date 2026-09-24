// Package kernel is the application's component framework: named component
// registration, dependency-ordered startup, guaranteed reverse-order teardown,
// and a global lifecycle state machine. It is a deliberately small Go analog of
// cordis's scope/service model (register, look up by name, dispose on teardown),
// keeping only what a single-binary agent CLI needs.
//
// The package exists so that correctness of lifecycle behavior is guaranteed by
// the framework (and machine-checked by its tests) instead of being re-derived
// by hand in every module. The invariants below are the whole contract; tests
// in this package assert each one, including a randomized property test for the
// ordering guarantee.
//
// # Invariants
//
// G1 Uniqueness. Component names are non-empty and unique. Violations are
// rejected at registration time (ErrEmptyName, ErrDuplicate), never at start.
//
// G2 Resolution. The start order is a topological order of the dependency DAG
// declared by Component.Deps: every component starts strictly after all of its
// declared dependencies. A missing dependency (ErrMissingDep) or a cycle
// (ErrCycle) fails resolution before any component has started.
//
// G3 Determinism. The same registration sequence always yields the same start
// order. No map iteration order or goroutine scheduling may influence it.
//
// G4 Rollback atomicity. If the k-th component fails to start (error or panic),
// every earlier component is stopped in exact reverse start order and the app
// enters Stopped. No component after the failure point is ever started. A
// component whose Start failed gets its Scope disposers run, but never Stop.
//
// G5 Teardown. Stop order is the exact reverse of the successful start order.
// Each Stop runs at most once. A Stop that errors or panics is recorded and
// teardown continues with the remaining components. Each Stop receives a
// context bounded by App.StopTimeout (cooperative: Stop must honor ctx).
//
// G6 Disposers. Scope.Defer registrations run exactly once, in LIFO order, when
// their component's start attempt ends: immediately on Start failure, or after
// Stop during teardown. Disposers run on a non-cancelable context so the
// shutdown signal cannot abort resource release. Disposers must be non-blocking.
//
// G7 Visibility and states. Component lookups (App.Component, kernel.Get) see
// only successfully started components: inside Start, exactly the declared
// dependencies are guaranteed visible. Legal state transitions are
//
//	New -> Starting -> Running -> Stopping -> Stopped
//	New -> Stopped                (Stop before Start)
//	Starting -> Stopped           (start failure or lifecycle cancel)
//
// and nothing else; an app is one-shot and never restarts. Registration is only
// legal in New.
//
// # Non-goals
//
// Dynamic plugin loading and hot reload (cordis's registry/effects runtime),
// service values decoupled from components (ctx.provide), and restart
// supervision (suture-style) are intentionally out of scope. See docs/kernel.md
// for the selection record and planned extensions.
package kernel
