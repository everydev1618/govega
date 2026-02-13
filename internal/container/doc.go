// Package container provides Docker container management for project isolation.
//
// The container package enables agents to execute tools in isolated Docker
// containers, providing security and reproducibility for agent operations.
//
// # Overview
//
// The package provides two main components:
//
//   - Manager: Low-level Docker container operations (start, stop, exec)
//   - ProjectRegistry: High-level project management with persistence
//
// # Graceful Degradation
//
// The container system is designed as an optional feature. When Docker is
// unavailable, Manager.IsAvailable() returns false and operations gracefully
// degrade to local execution.
//
// # Usage with Tools
//
// The container package integrates with govega's Tools system through the
// WithContainer and WithContainerRouting options, allowing specific tools
// to be routed to container execution while others run locally.
//
// # Example
//
//	// Create container manager
//	cm, err := container.NewManager(baseDir,
//	    container.WithNetworkName("my-network"),
//	    container.WithDefaultImage("node:20-slim"),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer cm.Close()
//
//	// Check availability
//	if cm.IsAvailable() {
//	    // Start a project container
//	    id, err := cm.StartProject(ctx, container.ContainerConfig{
//	        ProjectName: "my-project",
//	        Image:       "python:3.11-slim",
//	    })
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//
//	    // Execute a command
//	    result, err := cm.Exec(ctx, "my-project", []string{"python", "--version"}, "")
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    fmt.Println(result.Stdout)
//	}
package container
