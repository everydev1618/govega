// Package dsl provides a YAML-based domain-specific language for defining
// AI agent teams and workflows without writing Go code.
//
// # DSL Overview
//
// The DSL uses YAML files (typically named *.vega.yaml) to define:
//
//   - Agents: AI assistants with specific roles and capabilities
//   - Workflows: Multi-step processes that coordinate agents
//   - Tools: Custom tool definitions with various implementations
//   - Settings: Global configuration like rate limits and budgets
//
// # Basic Example
//
// A simple .vega.yaml file:
//
//	name: My Team
//
//	agents:
//	  assistant:
//	    model: claude-sonnet-4-20250514
//	    system: You are a helpful assistant.
//
//	workflows:
//	  greet:
//	    inputs:
//	      name:
//	        type: string
//	        required: true
//	    steps:
//	      - assistant:
//	          send: "Hello, {{name}}!"
//	          save: greeting
//	    output: "{{greeting}}"
//
// # Using the DSL
//
// Parse and execute a DSL file:
//
//	parser := dsl.NewParser()
//	doc, err := parser.ParseFile("team.vega.yaml")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	interp, err := dsl.NewInterpreter(doc)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer interp.Shutdown()
//
//	result, err := interp.Execute(ctx, "greet", map[string]any{
//	    "name": "World",
//	})
//
// # Expression Syntax
//
// The DSL supports {{expression}} interpolation:
//
//	{{variable}}           - Simple variable reference
//	{{step1.field}}        - Nested field access
//	{{value | upper}}      - Filter/transform
//	{{name | default:anon}} - Filter with argument
//
// Available filters: upper, lower, trim, default, lines, words, truncate, join
//
// # Control Flow
//
// The DSL supports various control structures:
//
//	# Conditionals
//	- if: "{{approved}}"
//	  then:
//	    - agent: ...
//	  else:
//	    - agent: ...
//
//	# Loops
//	- for: item in items
//	  steps:
//	    - agent:
//	        send: "Process {{item}}"
//
//	# Repeat until condition
//	- repeat:
//	    max: 5
//	    until: "'done' in result"
//	    steps:
//	      - agent: ...
//
//	# Parallel execution
//	- parallel:
//	    - agent1: ...
//	    - agent2: ...
//
// See the examples/ directory in the repository for complete examples.
package dsl
