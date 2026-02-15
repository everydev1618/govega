package vega

import (
	"sort"
	"sync"
	"testing"
)

func TestBlackboardSetGet(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("key1", "value1")
	g.BBSet("key2", 42)

	val, ok := g.BBGet("key1")
	if !ok || val != "value1" {
		t.Errorf("BBGet(key1) = %v, %v; want value1, true", val, ok)
	}

	val, ok = g.BBGet("key2")
	if !ok || val != 42 {
		t.Errorf("BBGet(key2) = %v, %v; want 42, true", val, ok)
	}

	_, ok = g.BBGet("nonexistent")
	if ok {
		t.Error("BBGet(nonexistent) should return false")
	}
}

func TestBlackboardDelete(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("key1", "value1")
	g.BBDelete("key1")

	_, ok := g.BBGet("key1")
	if ok {
		t.Error("BBGet after BBDelete should return false")
	}
}

func TestBlackboardKeys(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("b", 2)
	g.BBSet("a", 1)
	g.BBSet("c", 3)

	keys := g.BBKeys()
	sort.Strings(keys)
	if len(keys) != 3 || keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Errorf("BBKeys() = %v, want [a b c]", keys)
	}
}

func TestBlackboardSnapshot(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("k1", "v1")
	g.BBSet("k2", "v2")

	snap := g.BBSnapshot()
	if len(snap) != 2 {
		t.Errorf("BBSnapshot() length = %d, want 2", len(snap))
	}

	// Mutating snapshot should not affect blackboard
	snap["k3"] = "v3"
	_, ok := g.BBGet("k3")
	if ok {
		t.Error("mutating snapshot should not affect blackboard")
	}
}

func TestBlackboardConcurrent(t *testing.T) {
	g := NewGroup("test")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		key := "key"
		go func() {
			defer wg.Done()
			g.BBSet(key, "value")
		}()
		go func() {
			defer wg.Done()
			g.BBGet(key)
		}()
		go func() {
			defer wg.Done()
			g.BBKeys()
		}()
	}
	wg.Wait()
}
