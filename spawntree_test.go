package vega

import (
	"testing"
)

func TestGetSpawnTree_Empty(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))

	tree := o.GetSpawnTree()
	if len(tree) != 0 {
		t.Errorf("Empty orchestrator should have empty spawn tree, got %d nodes", len(tree))
	}
}

func TestGetSpawnTree_SingleRoot(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "RootAgent"}

	proc, _ := o.Spawn(agent, WithTask("root task"))

	tree := o.GetSpawnTree()
	if len(tree) != 1 {
		t.Fatalf("Expected 1 root node, got %d", len(tree))
	}

	root := tree[0]
	if root.ProcessID != proc.ID {
		t.Errorf("Root ProcessID = %q, want %q", root.ProcessID, proc.ID)
	}
	if root.AgentName != "RootAgent" {
		t.Errorf("Root AgentName = %q, want %q", root.AgentName, "RootAgent")
	}
	if root.Task != "root task" {
		t.Errorf("Root Task = %q, want %q", root.Task, "root task")
	}
	if root.Status != StatusRunning {
		t.Errorf("Root Status = %q, want %q", root.Status, StatusRunning)
	}
	if len(root.Children) != 0 {
		t.Errorf("Root should have no children, got %d", len(root.Children))
	}
}

func TestGetSpawnTree_ParentChild(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	parentAgent := Agent{Name: "Parent"}
	childAgent := Agent{Name: "Child"}

	parent, _ := o.Spawn(parentAgent, WithTask("parent task"))
	child, _ := o.Spawn(childAgent, WithParent(parent), WithSpawnReason("delegated subtask"))

	tree := o.GetSpawnTree()
	if len(tree) != 1 {
		t.Fatalf("Expected 1 root node, got %d", len(tree))
	}

	root := tree[0]
	if root.ProcessID != parent.ID {
		t.Errorf("Root should be parent, got %q", root.ProcessID)
	}
	if len(root.Children) != 1 {
		t.Fatalf("Parent should have 1 child, got %d", len(root.Children))
	}

	childNode := root.Children[0]
	if childNode.ProcessID != child.ID {
		t.Errorf("Child ProcessID = %q, want %q", childNode.ProcessID, child.ID)
	}
	if childNode.AgentName != "Child" {
		t.Errorf("Child AgentName = %q, want %q", childNode.AgentName, "Child")
	}
	if childNode.SpawnReason != "delegated subtask" {
		t.Errorf("Child SpawnReason = %q, want %q", childNode.SpawnReason, "delegated subtask")
	}
	if childNode.SpawnDepth != 1 {
		t.Errorf("Child SpawnDepth = %d, want 1", childNode.SpawnDepth)
	}
}

func TestGetSpawnTree_MultipleRoots(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))

	o.Spawn(Agent{Name: "Agent1"})
	o.Spawn(Agent{Name: "Agent2"})

	tree := o.GetSpawnTree()
	if len(tree) != 2 {
		t.Errorf("Expected 2 root nodes, got %d", len(tree))
	}
}

func TestGetSpawnTree_DeepNesting(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "Worker"}

	p1, _ := o.Spawn(agent)
	p2, _ := o.Spawn(agent, WithParent(p1))
	_, err := o.Spawn(agent, WithParent(p2))
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	tree := o.GetSpawnTree()
	if len(tree) != 1 {
		t.Fatalf("Expected 1 root, got %d", len(tree))
	}

	// Navigate tree
	if len(tree[0].Children) != 1 {
		t.Fatal("Root should have 1 child")
	}
	if len(tree[0].Children[0].Children) != 1 {
		t.Fatal("Child should have 1 grandchild")
	}
	if tree[0].Children[0].Children[0].SpawnDepth != 2 {
		t.Errorf("Grandchild SpawnDepth = %d, want 2", tree[0].Children[0].Children[0].SpawnDepth)
	}
}

func TestWithParent(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	parentAgent := Agent{Name: "Parent"}
	childAgent := Agent{Name: "Child"}

	parent, _ := o.Spawn(parentAgent)
	child, _ := o.Spawn(childAgent, WithParent(parent))

	if child.ParentID != parent.ID {
		t.Errorf("Child ParentID = %q, want %q", child.ParentID, parent.ID)
	}
	if child.ParentAgent != "Parent" {
		t.Errorf("Child ParentAgent = %q, want %q", child.ParentAgent, "Parent")
	}
	if child.SpawnDepth != 1 {
		t.Errorf("Child SpawnDepth = %d, want 1", child.SpawnDepth)
	}

	// Verify parent tracks child
	parent.childMu.RLock()
	defer parent.childMu.RUnlock()
	if len(parent.ChildIDs) != 1 || parent.ChildIDs[0] != child.ID {
		t.Error("Parent should track child ID")
	}
}

func TestWithParentNil(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "Test"}

	// Should not panic with nil parent
	proc, err := o.Spawn(agent, WithParent(nil))
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if proc.ParentID != "" {
		t.Error("ParentID should be empty with nil parent")
	}
}

func TestWithSpawnReason(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))
	agent := Agent{Name: "Test"}

	proc, _ := o.Spawn(agent, WithSpawnReason("testing spawn reason"))
	if proc.SpawnReason != "testing spawn reason" {
		t.Errorf("SpawnReason = %q, want %q", proc.SpawnReason, "testing spawn reason")
	}
}
