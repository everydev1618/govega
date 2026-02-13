package vega

import "time"

// SpawnTreeNode represents a node in the process spawn tree.
type SpawnTreeNode struct {
	ProcessID   string           `json:"process_id"`
	AgentName   string           `json:"agent_name"`
	Task        string           `json:"task"`
	Status      Status           `json:"status"`
	SpawnDepth  int              `json:"spawn_depth"`
	SpawnReason string           `json:"spawn_reason,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	Children    []*SpawnTreeNode `json:"children,omitempty"`
}

// GetSpawnTree returns the hierarchical spawn tree of all processes.
// Root processes (those with no parent) are returned as top-level nodes.
func (o *Orchestrator) GetSpawnTree() []*SpawnTreeNode {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// Build a map of process ID to node
	nodeMap := make(map[string]*SpawnTreeNode)
	for _, p := range o.processes {
		agentName := ""
		if p.Agent != nil {
			agentName = p.Agent.Name
		}
		nodeMap[p.ID] = &SpawnTreeNode{
			ProcessID:   p.ID,
			AgentName:   agentName,
			Task:        p.Task,
			Status:      p.Status(),
			SpawnDepth:  p.SpawnDepth,
			SpawnReason: p.SpawnReason,
			StartedAt:   p.StartedAt,
			Children:    make([]*SpawnTreeNode, 0),
		}
	}

	// Build the tree by connecting children to parents
	var roots []*SpawnTreeNode
	for _, p := range o.processes {
		node := nodeMap[p.ID]
		if p.ParentID == "" {
			// Root node
			roots = append(roots, node)
		} else if parent, ok := nodeMap[p.ParentID]; ok {
			// Connect to parent
			parent.Children = append(parent.Children, node)
		} else {
			// Parent not found (may have been cleaned up), treat as root
			roots = append(roots, node)
		}
	}

	return roots
}
