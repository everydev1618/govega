package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/serve"
)

func resetCmd(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	dbPath := fs.String("db", ".vega-serve.db", "SQLite database path")
	yes := fs.Bool("yes", false, "Skip confirmation prompt")

	fs.Usage = func() {
		fmt.Println(`Usage: vega reset [options]

Reset Vega to a fresh state by deleting all data.

This will delete:
  - All composed agents (Mother and Hermes are built-in and unaffected)
  - All chat history
  - All agent memory
  - All process events and snapshots
  - All workflow runs
  - All scheduled jobs
  - All file metadata
  - All workspace files on disk (~/.vega/workspace/)

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  vega reset
  vega reset --yes
  vega reset --db /path/to/custom.db`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	// Gather what will be deleted.
	workspace := vega.WorkspacePath()
	dbAbs, _ := filepath.Abs(*dbPath)

	var fileCount int
	filepath.Walk(workspace, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			fileCount++
		}
		return nil
	})

	// Count database records.
	store, err := serve.NewSQLiteStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database %s: %v\n", dbAbs, err)
		os.Exit(1)
	}

	type tableCount struct {
		label string
		table string
	}
	tables := []tableCount{
		{"Composed agents", "composed_agents"},
		{"Chat messages", "chat_messages"},
		{"Memory layers", "user_memory"},
		{"Memory items", "memory_items"},
		{"Events", "events"},
		{"Process snapshots", "process_snapshots"},
		{"Workflow runs", "workflow_runs"},
		{"Scheduled jobs", "scheduled_jobs"},
		{"File metadata", "workspace_files"},
	}

	fmt.Println("The following data will be deleted:")
	fmt.Println()
	totalRows := 0
	for _, tc := range tables {
		count := countRows(store, tc.table)
		totalRows += count
		if count > 0 {
			fmt.Printf("  %-22s %d records\n", tc.label, count)
		}
	}
	if fileCount > 0 {
		fmt.Printf("  %-22s %d files\n", "Workspace files", fileCount)
	}
	fmt.Println()
	fmt.Printf("  Database: %s\n", dbAbs)
	fmt.Printf("  Workspace: %s\n", workspace)
	fmt.Println()

	if totalRows == 0 && fileCount == 0 {
		fmt.Println("Nothing to reset — already clean.")
		store.Close()
		return
	}

	fmt.Println("Mother and Hermes are built-in and will NOT be affected.")
	fmt.Println()

	// Confirm unless --yes.
	if !*yes {
		fmt.Print("Are you sure you want to delete all of the above? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted.")
			store.Close()
			return
		}
		fmt.Println()
	}

	// Clear database tables.
	for _, tc := range tables {
		if err := deleteAll(store, tc.table); err != nil {
			fmt.Fprintf(os.Stderr, "  Error clearing %s: %v\n", tc.label, err)
		} else {
			fmt.Printf("  Cleared %s\n", tc.label)
		}
	}

	// Vacuum.
	vacuum(store)
	store.Close()

	// Clear workspace files.
	entries, err := os.ReadDir(workspace)
	if err == nil {
		for _, e := range entries {
			p := filepath.Join(workspace, e.Name())
			if err := os.RemoveAll(p); err != nil {
				fmt.Fprintf(os.Stderr, "  Error removing %s: %v\n", p, err)
			}
		}
		fmt.Printf("  Cleared workspace (%d files)\n", fileCount)
	}

	fmt.Println()
	fmt.Println("Reset complete. Vega is fresh.")
}

// countRows returns the number of rows in a table (best-effort, returns 0 on error).
func countRows(store *serve.SQLiteStore, table string) int {
	count, _ := store.CountTable(table)
	return count
}

func deleteAll(store *serve.SQLiteStore, table string) error {
	return store.DeleteAllFromTable(table)
}

func vacuum(store *serve.SQLiteStore) {
	store.Vacuum()
}
