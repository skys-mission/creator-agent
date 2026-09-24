package kernel

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestResolveEmpty(t *testing.T) {
	order, err := resolve(nil, nil)
	if err != nil {
		t.Fatalf("resolve(empty) err = %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("resolve(empty) order = %v, want none", order)
	}
}

func TestResolveChainAndDiamond(t *testing.T) {
	// Registration order intentionally scrambled; the DAG alone must decide.
	names := []string{"c", "a", "d", "b"}
	deps := map[string][]string{
		"c": {"b"},
		"b": {"a"},
		"d": {"a", "b"},
	}
	order, err := resolve(names, deps)
	if err != nil {
		t.Fatalf("resolve err = %v", err)
	}
	pos := positions(t, order, names)
	if !(pos["a"] < pos["b"] && pos["b"] < pos["c"]) {
		t.Fatalf("chain violated: order = %v", order)
	}
	if !(pos["a"] < pos["d"] && pos["b"] < pos["d"]) {
		t.Fatalf("diamond violated: order = %v", order)
	}
	if order[0] != "a" {
		t.Fatalf("order[0] = %q, want %q (only ready node)", order[0], "a")
	}
}

// TestResolvePropertyRandomDAG is the machine check behind invariants G2/G3:
// for random DAGs, the order is a valid topological order and is identical
// across repeated calls.
func TestResolvePropertyRandomDAG(t *testing.T) {
	const trials = 300
	for seed := uint64(0); seed < trials; seed++ {
		r := rand.New(rand.NewPCG(seed, 0x9e3779b979f47c15))
		n := 1 + r.IntN(40)
		names := make([]string, n)
		deps := make(map[string][]string, n)
		for i := 0; i < n; i++ {
			names[i] = "n" + strconv.Itoa(i)
			// Deps drawn from earlier nodes only: guaranteed acyclic.
			for j := 0; j < i; j++ {
				if r.IntN(3) == 0 {
					deps[names[i]] = append(deps[names[i]], names[j])
				}
			}
		}
		order, err := resolve(names, deps)
		if err != nil {
			t.Fatalf("seed %d: resolve err = %v", seed, err)
		}
		if len(order) != n {
			t.Fatalf("seed %d: order len = %d, want %d", seed, len(order), n)
		}
		pos := positions(t, order, names)
		for v, ds := range deps {
			for _, d := range ds {
				if pos[d] >= pos[v] {
					t.Fatalf("seed %d: %q depends on %q but starts at %d >= %d (order %v)",
						seed, v, d, pos[d], pos[v], order)
				}
			}
		}
		again, err := resolve(names, deps)
		if err != nil {
			t.Fatalf("seed %d: second resolve err = %v", seed, err)
		}
		if !slices.Equal(order, again) {
			t.Fatalf("seed %d: nondeterministic order:\n%v\n%v", seed, order, again)
		}
	}
}

func TestResolveMissingDep(t *testing.T) {
	_, err := resolve([]string{"a"}, map[string][]string{"a": {"ghost"}})
	if !errors.Is(err, ErrMissingDep) {
		t.Fatalf("err = %v, want ErrMissingDep", err)
	}
	for _, want := range []string{"a", "ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err %q missing %q", err, want)
		}
	}
}

func TestResolveCycleMessage(t *testing.T) {
	_, err := resolve([]string{"a", "b", "c"}, map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	if !strings.Contains(err.Error(), "a → b → c → a") {
		t.Fatalf("err %q missing concrete cycle", err)
	}
}

func TestResolveSelfCycle(t *testing.T) {
	_, err := resolve([]string{"a"}, map[string][]string{"a": {"a"}})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	if !strings.Contains(err.Error(), "a → a") {
		t.Fatalf("err %q missing self cycle", err)
	}
}

func TestResolveEmptyDepName(t *testing.T) {
	_, err := resolve([]string{"a"}, map[string][]string{"a": {"  "}})
	if err == nil || !strings.Contains(err.Error(), "empty dependency name") {
		t.Fatalf("err = %v, want empty-dependency error", err)
	}
}

func TestResolveDuplicateDepIgnored(t *testing.T) {
	order, err := resolve([]string{"a", "b"}, map[string][]string{"a": {"b", "b"}})
	if err != nil {
		t.Fatalf("resolve err = %v", err)
	}
	if !slices.Equal(order, []string{"b", "a"}) {
		t.Fatalf("order = %v, want [b a]", order)
	}
}

func positions(t *testing.T, order, names []string) map[string]int {
	t.Helper()
	if len(order) != len(names) {
		t.Fatalf("order %v is not a permutation of %v", order, names)
	}
	pos := make(map[string]int, len(order))
	for i, n := range order {
		if _, dup := pos[n]; dup {
			t.Fatalf("duplicate %q in order %v", n, order)
		}
		pos[n] = i
	}
	if len(pos) != len(names) {
		t.Fatalf("order %v is not a permutation of %v", order, names)
	}
	return pos
}
