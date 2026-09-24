package kernel

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Resolution errors. Both are wrapped with context and testable with errors.Is.
var (
	// ErrMissingDep reports a dependency on a component that was never registered.
	ErrMissingDep = errors.New("kernel: unregistered dependency")
	// ErrCycle reports a dependency cycle; the message spells out one concrete cycle.
	ErrCycle = errors.New("kernel: dependency cycle")
)

// resolve computes the deterministic topological start order (G2/G3) for the
// components named by names (registration order) with dependency lists deps
// (name -> declared dependency names). It is Kahn's algorithm with a FIFO ready
// queue seeded in registration order and dependents visited in registration
// order, so the result depends only on the inputs.
//
// On a missing dependency or a cycle it returns a diagnostic error and no order;
// callers must not have started anything (G2).
func resolve(names []string, deps map[string][]string) ([]string, error) {
	index := make(map[string]int, len(names))
	for i, n := range names {
		index[n] = i
	}

	// dependents[u] lists, in registration order, the components that declare u
	// as a dependency; inDeg[v] counts v's distinct declared dependencies.
	dependents := make([][]int, len(names))
	inDeg := make([]int, len(names))
	for v, n := range names {
		seen := make(map[string]bool, len(deps[n]))
		for _, d := range deps[n] {
			d = strings.TrimSpace(d)
			if d == "" {
				return nil, fmt.Errorf("kernel: component %q declares an empty dependency name", n)
			}
			if seen[d] {
				continue
			}
			seen[d] = true
			u, ok := index[d]
			if !ok {
				return nil, fmt.Errorf("%w: component %q depends on %q, which is not registered", ErrMissingDep, n, d)
			}
			dependents[u] = append(dependents[u], v)
			inDeg[v]++
		}
	}

	ready := make([]int, 0, len(names))
	for i := range names {
		if inDeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	order := make([]string, 0, len(names))
	for len(ready) > 0 {
		u := ready[0]
		ready = ready[1:]
		order = append(order, names[u])
		for _, v := range dependents[u] {
			inDeg[v]--
			if inDeg[v] == 0 {
				ready = append(ready, v)
			}
		}
	}
	if len(order) < len(names) {
		return nil, cycleError(names, deps, order)
	}
	return order, nil
}

// cycleError extracts one concrete cycle from the nodes that Kahn's algorithm
// could not place and formats it as "a → b → a" (arrows read "depends on").
// Deterministic: DFS from stuck nodes in registration order, edges in
// declaration order.
func cycleError(names []string, deps map[string][]string, placed []string) error {
	index := make(map[string]int, len(names))
	for i, n := range names {
		index[n] = i
	}
	done := make(map[string]bool, len(placed))
	for _, n := range placed {
		done[n] = true
	}

	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make([]int, len(names))
	path := make([]int, 0, len(names))
	var cycle []int

	var dfs func(u int) bool
	dfs = func(u int) bool {
		color[u] = grey
		path = append(path, u)
		for _, d := range deps[names[u]] {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			v, ok := index[d]
			if !ok || done[names[v]] {
				continue
			}
			switch color[v] {
			case grey:
				i := slices.Index(path, v)
				cycle = append(slices.Clone(path[i:]), v)
				return true
			case white:
				if dfs(v) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		color[u] = black
		return false
	}

	for u := range names {
		if done[names[u]] || color[u] != white {
			continue
		}
		if dfs(u) {
			break
		}
	}

	parts := make([]string, 0, len(cycle))
	for _, u := range cycle {
		parts = append(parts, names[u])
	}
	if len(parts) == 0 {
		// Unreachable: any unplaced node is on a cycle or depends on one.
		return fmt.Errorf("%w: undetermined cycle among %d components", ErrCycle, len(names)-len(placed))
	}
	return fmt.Errorf("%w: %s", ErrCycle, strings.Join(parts, " → "))
}
