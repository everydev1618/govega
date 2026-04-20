// Package main provides the Vega CLI.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/everydev1618/govega/dsl"
)

var (
	version = "dev"
)

func main() {
	loadEnvFile()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		initCmd()
	case "generate":
		generateCmd(args)
	case "run":
		runCmd(args)
	case "validate":
		validateCmd(args)
	case "repl":
		replCmd(args)
	case "serve":
		serveCmd(args)
	case "reset":
		resetCmd(args)
	case "version":
		fmt.Printf("vega %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Vega - AI Agent Orchestration

Usage:
  vega <command> [options]

Commands:
  init      Set up API keys and configuration
  generate  Generate an agent from a description or population components
  run       Run a workflow from a .vega.yaml file
  validate  Validate a .vega.yaml file
  repl      Interactive REPL for exploring agents
  serve     Start web dashboard and REST API server
  reset     Delete all agents, files, chat history, and memory
  version   Print version information
  help      Show this help message

Examples:
  vega init
  vega generate "world class devops agent" --format claude-skill
  vega run team.vega.yaml --workflow code-review --task "Build a REST API"
  vega validate team.vega.yaml
  vega repl team.vega.yaml
  vega serve
  vega serve team.vega.yaml --addr :8080

Run 'vega <command> --help' for more information on a command.`)
}

// runCmd executes a workflow from a .vega.yaml file.
func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	workflow := fs.String("workflow", "", "Workflow to execute")
	task := fs.String("task", "", "Task description to pass to workflow")
	timeout := fs.Duration("timeout", 30*time.Minute, "Maximum execution time")
	output := fs.String("output", "", "Output format: json, yaml, or text (default)")
	inputFile := fs.String("input", "", "JSON file containing workflow inputs")
	verbose := fs.Bool("verbose", false, "Enable verbose output")

	fs.Usage = func() {
		fmt.Println(`Usage: vega run <file.vega.yaml> [options]

Run a workflow from a .vega.yaml file.

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  vega run team.vega.yaml --workflow code-review --task "Build a REST API"
  vega run team.vega.yaml --workflow process-data --input params.json`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	requireAPIKey()

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: no .vega.yaml file specified")
		fs.Usage()
		os.Exit(1)
	}

	file := fs.Arg(0)

	// Parse the file
	parser := dsl.NewParser()
	doc, err := parser.ParseFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", file, err)
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("Loaded %s: %d agents, %d workflows\n",
			doc.Name, len(doc.Agents), len(doc.Workflows))
	}

	// Determine which workflow to run
	workflowName := *workflow
	if workflowName == "" {
		// If only one workflow, use it
		if len(doc.Workflows) == 1 {
			for name := range doc.Workflows {
				workflowName = name
			}
		} else {
			fmt.Fprintln(os.Stderr, "Error: multiple workflows found, specify one with --workflow")
			fmt.Fprintln(os.Stderr, "Available workflows:")
			for name := range doc.Workflows {
				fmt.Fprintf(os.Stderr, "  - %s\n", name)
			}
			os.Exit(1)
		}
	}

	// Check workflow exists
	wf, ok := doc.Workflows[workflowName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: workflow '%s' not found\n", workflowName)
		fmt.Fprintln(os.Stderr, "Available workflows:")
		for name := range doc.Workflows {
			fmt.Fprintf(os.Stderr, "  - %s\n", name)
		}
		os.Exit(1)
	}

	// Build inputs
	inputs := make(map[string]any)

	// Load from file if specified
	if *inputFile != "" {
		data, err := os.ReadFile(*inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(data, &inputs); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing input file: %v\n", err)
			os.Exit(1)
		}
	}

	// Override with --task if provided
	if *task != "" {
		inputs["task"] = *task
	}

	// Validate required inputs
	for name, input := range wf.Inputs {
		if input.Required {
			if _, ok := inputs[name]; !ok {
				if input.Default != nil {
					inputs[name] = input.Default
				} else {
					fmt.Fprintf(os.Stderr, "Error: required input '%s' not provided\n", name)
					os.Exit(1)
				}
			}
		}
	}

	// Create interpreter
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating interpreter: %v\n", err)
		os.Exit(1)
	}
	defer interp.Shutdown()

	if *verbose {
		fmt.Printf("Running workflow: %s\n", workflowName)
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := interp.Execute(ctx, workflowName, inputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Output result
	switch *output {
	case "json":
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	case "yaml":
		// Simple YAML output for primitives
		switch v := result.(type) {
		case string:
			fmt.Println(v)
		case map[string]any:
			data, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(data))
		default:
			fmt.Printf("%v\n", result)
		}
	default:
		fmt.Printf("%v\n", result)
	}
}

// validateCmd validates a .vega.yaml file.
func validateCmd(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	verbose := fs.Bool("verbose", false, "Show detailed validation results")

	fs.Usage = func() {
		fmt.Println(`Usage: vega validate <file.vega.yaml> [options]

Validate a .vega.yaml file without executing it.

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  vega validate team.vega.yaml
  vega validate team.vega.yaml --verbose`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: no .vega.yaml file specified")
		fs.Usage()
		os.Exit(1)
	}

	file := fs.Arg(0)

	// Parse and validate
	parser := dsl.NewParser()
	doc, err := parser.ParseFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Validation failed: %v\n", err)
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("File: %s\n", file)
		fmt.Printf("Name: %s\n", doc.Name)
		if doc.Description != "" {
			fmt.Printf("Description: %s\n", doc.Description)
		}
		fmt.Println()

		fmt.Printf("Agents (%d):\n", len(doc.Agents))
		for name, agent := range doc.Agents {
			model := agent.Model
			if model == "" {
				model = "(default)"
			}
			fmt.Printf("  - %s: model=%s\n", name, model)
		}
		fmt.Println()

		fmt.Printf("Workflows (%d):\n", len(doc.Workflows))
		for name, wf := range doc.Workflows {
			fmt.Printf("  - %s: %d steps\n", name, len(wf.Steps))
			if len(wf.Inputs) > 0 {
				fmt.Printf("    inputs: ")
				inputNames := make([]string, 0, len(wf.Inputs))
				for n := range wf.Inputs {
					inputNames = append(inputNames, n)
				}
				fmt.Println(strings.Join(inputNames, ", "))
			}
		}
		fmt.Println()

		if doc.Settings != nil {
			fmt.Println("Settings:")
			if doc.Settings.DefaultModel != "" {
				fmt.Printf("  default_model: %s\n", doc.Settings.DefaultModel)
			}
			if doc.Settings.Sandbox != "" {
				fmt.Printf("  sandbox: %s\n", doc.Settings.Sandbox)
			}
			if doc.Settings.Budget != "" {
				fmt.Printf("  budget: %s\n", doc.Settings.Budget)
			}
		}
	}

	fmt.Printf("Valid: %s\n", file)
}

// replCmd starts an interactive REPL.
func replCmd(args []string) {
	fs := flag.NewFlagSet("repl", flag.ExitOnError)

	fs.Usage = func() {
		fmt.Println(`Usage: vega repl [file.vega.yaml]

Start an interactive REPL for exploring agents.

If a .vega.yaml file is provided, agents and workflows will be pre-loaded.

Commands:
  /agents          List available agents
  /workflows       List available workflows
  /run <workflow>  Run a workflow
  /ask <agent>     Start a conversation with an agent
  /help            Show REPL help
  /quit            Exit the REPL`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	requireAPIKey()

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Usage: vega repl <file.vega.yaml>")
		os.Exit(1)
	}

	file := fs.Arg(0)
	parser := dsl.NewParser()
	doc, err := parser.ParseFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading %s: %v\n", file, err)
		os.Exit(1)
	}

	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating interpreter: %v\n", err)
		os.Exit(1)
	}
	defer interp.Shutdown()

	fmt.Printf("Loaded: %s (%d agents, %d workflows)\n",
		doc.Name, len(doc.Agents), len(doc.Workflows))

	repl := dsl.NewREPL(interp, dsl.WithREPLPrompt("vega"))
	repl.Run()
}

// requireAPIKey checks that ANTHROPIC_API_KEY is set (loadEnvFile must have run
// first). If missing it prints a friendly error and exits.
func requireAPIKey() {
	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENAI_BASE_URL") != "" {
		return
	}
	fmt.Fprintln(os.Stderr, `Error: No LLM backend configured.

Set one of:
  1. ANTHROPIC_API_KEY for the Anthropic API
  2. OPENAI_BASE_URL for an OpenAI-compatible endpoint (LiteLLM, Ollama, etc.)

Configure via 'vega init', ~/.vega/env, or environment variables.`)
	os.Exit(1)
}

// loadEnvFile reads ~/.vega/env and sets any key=value pairs as environment
// variables. Existing env vars take precedence (won't be overwritten).
// Lines starting with # are comments; blank lines are ignored.
func loadEnvFile() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	f, err := os.Open(home + "/.vega/env")
	if err != nil {
		return // file doesn't exist, that's fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Don't overwrite existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

