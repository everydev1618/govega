package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
)

func initCmd() {
	fmt.Println(`
  ✦  Vega Setup
  ─────────────────────────────`)

	home := vega.Home()
	envPath := home + "/env"

	// Load existing keys if env file exists.
	existing := loadExistingEnv(envPath)
	if len(existing) > 0 {
		fmt.Println("\n  Found existing configuration at", envPath)
		for k, v := range existing {
			fmt.Printf("    %s = %s\n", k, maskKey(v))
		}
		fmt.Println()
		if !confirm("  Reconfigure?") {
			fmt.Println("\n  Keeping existing configuration. You're all set!")
			printNextSteps()
			return
		}
	}

	scanner := bufio.NewScanner(os.Stdin)

	// Anthropic API key (required).
	fmt.Println("\n  Anthropic API key (required)")
	fmt.Println("  Get one at: https://console.anthropic.com/settings/keys")
	fmt.Print("\n  ANTHROPIC_API_KEY: ")
	var apiKey string
	if scanner.Scan() {
		apiKey = strings.TrimSpace(scanner.Text())
	}

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "\n  Error: API key is required. Run 'vega init' to try again.")
		os.Exit(1)
	}

	// Validate the key.
	fmt.Print("  Validating key... ")
	client := llm.NewAnthropic(llm.WithAPIKey(apiKey))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := client.ValidateKey(ctx)
	cancel()

	if err != nil {
		fmt.Println("failed")
		fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "  Please check the key and try again.")
		os.Exit(1)
	}
	fmt.Println("valid!")

	// Telegram bot token (optional).
	fmt.Println("\n  Telegram bot token (optional — press Enter to skip)")
	fmt.Println("  Create a bot via @BotFather on Telegram")
	fmt.Print("\n  TELEGRAM_BOT_TOKEN: ")
	var telegramToken string
	if scanner.Scan() {
		telegramToken = strings.TrimSpace(scanner.Text())
	}

	// Ensure ~/.vega/ and ~/.vega/workspace/ exist.
	if err := vega.EnsureHome(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Error creating %s: %v\n", home, err)
		os.Exit(1)
	}

	// Merge: only overwrite keys the user provided.
	if apiKey != "" {
		existing["ANTHROPIC_API_KEY"] = apiKey
	}
	if telegramToken != "" {
		existing["TELEGRAM_BOT_TOKEN"] = telegramToken
	}

	if err := writeEnvFile(envPath, existing); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Error writing %s: %v\n", envPath, err)
		os.Exit(1)
	}

	fmt.Printf("\n  Configuration saved to %s\n", envPath)
	printNextSteps()
}

func printNextSteps() {
	fmt.Print(`
  Next steps:
    vega serve          Start the web UI and agent dashboard
    vega repl           Interactive REPL for exploring agents
    vega run <file>     Run a workflow from a .vega.yaml file
`)
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
	return false
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func loadExistingEnv(path string) map[string]string {
	env := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return env
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
		env[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return env
}

func writeEnvFile(path string, env map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	w.WriteString("# Vega configuration — managed by 'vega init'\n")

	// Write in a stable order: known keys first.
	order := []string{"ANTHROPIC_API_KEY", "TELEGRAM_BOT_TOKEN"}
	written := make(map[string]bool)
	for _, k := range order {
		if v, ok := env[k]; ok && v != "" {
			fmt.Fprintf(w, "%s=%s\n", k, v)
			written[k] = true
		}
	}
	// Write any other keys that may have been in the original file.
	for k, v := range env {
		if !written[k] && v != "" {
			fmt.Fprintf(w, "%s=%s\n", k, v)
		}
	}

	return w.Flush()
}
