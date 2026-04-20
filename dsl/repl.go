package dsl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// REPL provides an interactive terminal chat for a Vega interpreter.
type REPL struct {
	interp       *Interpreter
	in           io.Reader
	out          io.Writer
	prompt       string
	sendTimeout  time.Duration
}

// REPLOption configures a REPL.
type REPLOption func(*REPL)

// WithREPLInput sets the input reader (default: os.Stdin).
func WithREPLInput(r io.Reader) REPLOption {
	return func(repl *REPL) { repl.in = r }
}

// WithREPLOutput sets the output writer (default: os.Stdout).
func WithREPLOutput(w io.Writer) REPLOption {
	return func(repl *REPL) { repl.out = w }
}

// WithREPLPrompt sets the app name shown in the prompt (default: "vega").
func WithREPLPrompt(name string) REPLOption {
	return func(repl *REPL) { repl.prompt = name }
}

// WithREPLTimeout sets the timeout for agent messages (default: 5 minutes).
func WithREPLTimeout(d time.Duration) REPLOption {
	return func(repl *REPL) { repl.sendTimeout = d }
}

// NewREPL creates a new REPL for the given interpreter.
func NewREPL(interp *Interpreter, opts ...REPLOption) *REPL {
	repl := &REPL{
		interp:      interp,
		in:          os.Stdin,
		out:         os.Stdout,
		prompt:      "vega",
		sendTimeout: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(repl)
	}
	return repl
}

// Run starts the interactive REPL loop.
func (r *REPL) Run() {
	doc := r.interp.Document()
	scanner := bufio.NewScanner(r.in)
	var currentAgent string

	// Auto-select if there's only one agent.
	if doc != nil && len(doc.Agents) == 1 {
		for name := range doc.Agents {
			currentAgent = name
		}
		fmt.Fprintf(r.out, "Chatting with %s. Type /help for commands, /quit to exit.\n\n", currentAgent)
	} else {
		fmt.Fprintf(r.out, "Type /help for commands, /quit to exit.\n\n")
	}

	for {
		if currentAgent != "" {
			fmt.Fprintf(r.out, "[%s]> ", currentAgent)
		} else {
			fmt.Fprintf(r.out, "%s> ", r.prompt)
		}

		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			if r.handleCommand(line, &currentAgent) {
				return
			}
			continue
		}

		if currentAgent != "" {
			r.sendMessage(currentAgent, line)
		} else {
			fmt.Fprintln(r.out, "No agent selected. Use /ask <agent> to start a conversation.")
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(r.out, "Error reading input: %v\n", err)
	}
}

// handleCommand processes a slash command. Returns true if the REPL should exit.
func (r *REPL) handleCommand(line string, currentAgent *string) bool {
	parts := strings.Fields(line)
	cmd := parts[0]
	doc := r.interp.Document()

	switch cmd {
	case "/quit", "/exit", "/q":
		fmt.Fprintln(r.out, "Goodbye!")
		return true

	case "/help", "/h":
		r.printHelp()

	case "/agents":
		if doc == nil {
			fmt.Fprintln(r.out, "No agents loaded.")
			return false
		}
		fmt.Fprintln(r.out, "Agents:")
		for name, agent := range doc.Agents {
			model := agent.Model
			if model == "" {
				model = "(default)"
			}
			fmt.Fprintf(r.out, "  %s - %s\n", name, model)
		}

	case "/ask":
		if len(parts) < 2 {
			fmt.Fprintln(r.out, "Usage: /ask <agent>")
			return false
		}
		agentName := parts[1]
		if doc == nil || doc.Agents[agentName] == nil {
			fmt.Fprintf(r.out, "Agent '%s' not found.\n", agentName)
			return false
		}
		*currentAgent = agentName
		fmt.Fprintf(r.out, "Now talking to %s. Type /end to stop.\n", agentName)

	case "/end":
		*currentAgent = ""
		fmt.Fprintln(r.out, "Ended conversation.")

	case "/workflows":
		if doc == nil {
			fmt.Fprintln(r.out, "No workflows loaded.")
			return false
		}
		fmt.Fprintln(r.out, "Workflows:")
		for name, wf := range doc.Workflows {
			desc := wf.Description
			if desc == "" {
				desc = fmt.Sprintf("%d steps", len(wf.Steps))
			}
			fmt.Fprintf(r.out, "  %s - %s\n", name, desc)
		}

	case "/run":
		if len(parts) < 2 {
			fmt.Fprintln(r.out, "Usage: /run <workflow> [task]")
			return false
		}
		inputs := make(map[string]any)
		if len(parts) > 2 {
			inputs["task"] = strings.Join(parts[2:], " ")
		}
		ctx, cancel := context.WithTimeout(context.Background(), r.sendTimeout)
		result, err := r.interp.Execute(ctx, parts[1], inputs)
		cancel()
		if err != nil {
			fmt.Fprintf(r.out, "Error: %v\n", err)
		} else {
			fmt.Fprintf(r.out, "Result:\n%v\n", result)
		}

	default:
		fmt.Fprintf(r.out, "Unknown command: %s. Type /help for available commands.\n", cmd)
	}

	return false
}

func (r *REPL) sendMessage(agent, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), r.sendTimeout)
	defer cancel()

	response, err := r.interp.SendToAgent(ctx, agent, message)
	if err != nil {
		fmt.Fprintf(r.out, "Error: %v\n", err)
	} else {
		fmt.Fprintf(r.out, "\n%s\n\n", response)
	}
}

func (r *REPL) printHelp() {
	fmt.Fprintln(r.out, `Commands:
  /agents          List available agents
  /workflows       List available workflows
  /ask <agent>     Start a conversation with an agent
  /end             End current conversation
  /run <wf> [task] Run a workflow
  /help            Show this help
  /quit            Exit

When in a conversation (after /ask):
  Type your message and press Enter to send it to the agent.
  Use /end to stop the conversation.`)
}
