package vega

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// ---------- Blackboard: Basic CRUD ----------

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

func TestBlackboardOverwrite(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("key", "original")
	g.BBSet("key", "updated")

	val, ok := g.BBGet("key")
	if !ok || val != "updated" {
		t.Errorf("BBGet after overwrite = %v, %v; want updated, true", val, ok)
	}
}

func TestBlackboardMixedTypes(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("string", "hello")
	g.BBSet("int", 42)
	g.BBSet("float", 3.14)
	g.BBSet("bool", true)
	g.BBSet("slice", []string{"a", "b"})
	g.BBSet("map", map[string]int{"x": 1})

	if val, ok := g.BBGet("string"); !ok || val != "hello" {
		t.Errorf("string = %v, %v", val, ok)
	}
	if val, ok := g.BBGet("int"); !ok || val != 42 {
		t.Errorf("int = %v, %v", val, ok)
	}
	if val, ok := g.BBGet("float"); !ok || val != 3.14 {
		t.Errorf("float = %v, %v", val, ok)
	}
	if val, ok := g.BBGet("bool"); !ok || val != true {
		t.Errorf("bool = %v, %v", val, ok)
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

func TestBlackboardDeleteNonexistent(t *testing.T) {
	g := NewGroup("test")
	// Should not panic
	g.BBDelete("does-not-exist")
}

func TestBlackboardDeleteThenReAdd(t *testing.T) {
	g := NewGroup("test")

	g.BBSet("key", "v1")
	g.BBDelete("key")
	g.BBSet("key", "v2")

	val, ok := g.BBGet("key")
	if !ok || val != "v2" {
		t.Errorf("BBGet after delete+re-add = %v, %v; want v2, true", val, ok)
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

func TestBlackboardKeysEmpty(t *testing.T) {
	g := NewGroup("test")
	keys := g.BBKeys()
	if len(keys) != 0 {
		t.Errorf("BBKeys on empty board = %v, want empty", keys)
	}
}

func TestBlackboardKeysAfterDelete(t *testing.T) {
	g := NewGroup("test")
	g.BBSet("a", 1)
	g.BBSet("b", 2)
	g.BBDelete("a")

	keys := g.BBKeys()
	if len(keys) != 1 || keys[0] != "b" {
		t.Errorf("BBKeys after delete = %v, want [b]", keys)
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

func TestBlackboardSnapshotEmpty(t *testing.T) {
	g := NewGroup("test")
	snap := g.BBSnapshot()
	if snap == nil {
		t.Fatal("BBSnapshot on empty board should return non-nil map")
	}
	if len(snap) != 0 {
		t.Errorf("BBSnapshot on empty board length = %d, want 0", len(snap))
	}
}

func TestBlackboardSnapshotIsShallowCopy(t *testing.T) {
	g := NewGroup("test")
	g.BBSet("k1", "v1")

	snap1 := g.BBSnapshot()
	g.BBSet("k2", "v2")
	snap2 := g.BBSnapshot()

	if len(snap1) != 1 {
		t.Errorf("snap1 should have 1 key, got %d", len(snap1))
	}
	if len(snap2) != 2 {
		t.Errorf("snap2 should have 2 keys, got %d", len(snap2))
	}
}

// ---------- Blackboard: Concurrency ----------

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

func TestBlackboardConcurrentMultiKey(t *testing.T) {
	g := NewGroup("test")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		key := fmt.Sprintf("key-%d", i)
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
			g.BBSnapshot()
		}()
		go func() {
			defer wg.Done()
			g.BBDelete(key)
		}()
	}
	wg.Wait()
}
