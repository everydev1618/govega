package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/serve"
)

// defaultDocument returns a minimal Document for when no YAML file is provided.
// Mother and Hermes are always injected by the server, so no starter agents
// are needed here.
func defaultDocument() *dsl.Document {
	return &dsl.Document{
		Name:      "Vega",
		Agents:    map[string]*dsl.Agent{},
		Workflows: map[string]*dsl.Workflow{},
	}
}

// serveCmd starts the web dashboard and REST API server.
func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":3001", "HTTP listen address")
	dbPath := fs.String("db", ".vega-serve.db", "SQLite database path")

	fs.Usage = func() {
		fmt.Println(`Usage: vega serve [file.vega.yaml] [options]

Start a web dashboard and REST API server for monitoring and controlling agents.

If no YAML file is provided, a default configuration with a basic assistant
agent is used.

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  vega serve
  vega serve team.vega.yaml
  vega serve team.vega.yaml --addr :8080
  vega serve team.vega.yaml --db /tmp/vega.db`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	requireAPIKey()

	var doc *dsl.Document

	if fs.NArg() >= 1 {
		file := fs.Arg(0)
		parser := dsl.NewParser()
		var err error
		doc, err = parser.ParseFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", file, err)
			os.Exit(1)
		}
	} else {
		doc = defaultDocument()
	}

	// Create interpreter with lazy spawn — agents are created on first use.
	interp, err := dsl.NewInterpreter(doc, dsl.WithLazySpawn())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating interpreter: %v\n", err)
		os.Exit(1)
	}
	defer interp.Shutdown()

	fmt.Printf("Loaded: %s (%d agents, %d workflows)\n",
		doc.Name, len(doc.Agents), len(doc.Workflows))

	// Create and start server
	cfg := serve.Config{
		Addr:          *addr,
		DBPath:        *dbPath,
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramAgent: os.Getenv("TELEGRAM_AGENT"),
	}

	srv := serve.New(interp, cfg)

	// Signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
