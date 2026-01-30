// Package vega provides fault-tolerant AI agent orchestration with Erlang-style supervision.
//
// Vega is a Go library for building reliable AI agent systems. It provides:
//
//   - Agent definitions with configurable LLM backends
//   - Process management with lifecycle control
//   - Erlang-style supervision trees for fault tolerance
//   - Tool registration with sandboxing
//   - Rate limiting and circuit breakers
//   - Budget management for cost control
//   - A YAML-based DSL for non-programmers
//
// # Quick Start
//
// Create an agent and spawn a process:
//
//	// Create an orchestrator with Anthropic backend
//	llm := llm.NewAnthropic()
//	orch := vega.NewOrchestrator(vega.WithLLM(llm))
//
//	// Define an agent
//	agent := vega.Agent{
//	    Name:   "assistant",
//	    Model:  "claude-sonnet-4-20250514",
//	    System: vega.StaticPrompt("You are a helpful assistant."),
//	}
//
//	// Spawn a process
//	proc, err := orch.Spawn(agent)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Send a message
//	response, err := proc.Send(ctx, "Hello!")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(response)
//
// # Supervision
//
// Add fault tolerance with supervision strategies:
//
//	proc, err := orch.Spawn(agent, vega.WithSupervision(vega.Supervision{
//	    Strategy:    vega.Restart,
//	    MaxRestarts: 3,
//	    Window:      5 * time.Minute,
//	}))
//
// Supervision strategies:
//   - Restart: Automatically restart the process on failure
//   - Stop: Stop the process permanently on failure
//   - Escalate: Pass the failure to a supervisor agent
//
// # Tools
//
// Register tools for agents to use:
//
//	tools := vega.NewTools()
//	tools.RegisterBuiltins() // read_file, write_file, run_command
//
//	tools.Register("greet", func(name string) string {
//	    return "Hello, " + name + "!"
//	})
//
//	agent := vega.Agent{
//	    Name:  "greeter",
//	    Tools: tools,
//	}
//
// # DSL
//
// For non-programmers, use the YAML-based DSL in the dsl package:
//
//	parser := dsl.NewParser()
//	doc, err := parser.ParseFile("team.vega.yaml")
//
//	interp, err := dsl.NewInterpreter(doc)
//	result, err := interp.Execute(ctx, "workflow-name", inputs)
//
// See the examples/ directory for complete DSL examples.
//
// # Architecture
//
// The main components are:
//
//   - Agent: Blueprint defining model, system prompt, tools, and configuration
//   - Process: A running agent instance with state and lifecycle
//   - Orchestrator: Manages processes, enforces limits, coordinates shutdown
//   - Supervision: Fault tolerance configuration with restart strategies
//   - Tools: Tool registration with schema generation and sandboxing
//   - LLM: Interface for language model backends (Anthropic provided)
//
// # Thread Safety
//
// All exported types are safe for concurrent use. The Orchestrator and Process
// types use internal synchronization to protect shared state.
package vega
