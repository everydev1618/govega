package tools

import (
	"context"
	"os"
	"testing"
)

func TestMssqlServerToolDefinitions(t *testing.T) {
	srv := mssqlServer()
	if srv == nil {
		t.Fatal("mssqlServer() returned nil")
	}

	expected := []string{"list_tables", "describe_table", "execute_query", "execute_statement"}
	for _, name := range expected {
		td, ok := srv.tools[name]
		if !ok {
			t.Errorf("missing tool %q", name)
			continue
		}
		if td.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}
		if td.Fn == nil {
			t.Errorf("tool %q has nil Fn", name)
		}
	}

	if len(srv.tools) != len(expected) {
		t.Errorf("expected %d tools, got %d", len(expected), len(srv.tools))
	}
}

func TestMssqlServerDescribeTableRequiresParam(t *testing.T) {
	td := mssqlServer().tools["describe_table"]
	p, ok := td.Params["table"]
	if !ok {
		t.Fatal("describe_table missing 'table' param")
	}
	if !p.Required {
		t.Error("describe_table 'table' param should be required")
	}
}

func TestMssqlServerExecuteQueryRequiresParam(t *testing.T) {
	td := mssqlServer().tools["execute_query"]
	p, ok := td.Params["query"]
	if !ok {
		t.Fatal("execute_query missing 'query' param")
	}
	if !p.Required {
		t.Error("execute_query 'query' param should be required")
	}
}

func TestMssqlServerExecuteStatementRequiresParam(t *testing.T) {
	td := mssqlServer().tools["execute_statement"]
	p, ok := td.Params["statement"]
	if !ok {
		t.Fatal("execute_statement missing 'statement' param")
	}
	if !p.Required {
		t.Error("execute_statement 'statement' param should be required")
	}
}

func TestMssqlConnectBuiltinServer(t *testing.T) {
	tools := NewTools()
	n, err := tools.ConnectBuiltinServer(context.Background(), "mssql")
	if err != nil {
		t.Fatalf("ConnectBuiltinServer: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 tools registered, got %d", n)
	}

	// Verify tools are registered with prefix.
	schemas := tools.Schema()
	prefixed := make(map[string]bool)
	for _, s := range schemas {
		prefixed[s.Name] = true
	}

	for _, name := range []string{"mssql__list_tables", "mssql__describe_table", "mssql__execute_query", "mssql__execute_statement"} {
		if !prefixed[name] {
			t.Errorf("expected tool %q in schema", name)
		}
	}
}

func TestMssqlBuiltinServerConnected(t *testing.T) {
	tools := NewTools()

	if tools.BuiltinServerConnected("mssql") {
		t.Error("should not be connected before ConnectBuiltinServer")
	}

	tools.ConnectBuiltinServer(context.Background(), "mssql")

	if !tools.BuiltinServerConnected("mssql") {
		t.Error("should be connected after ConnectBuiltinServer")
	}
}

func TestMssqlDisconnectBuiltinServer(t *testing.T) {
	tools := NewTools()
	tools.ConnectBuiltinServer(context.Background(), "mssql")

	if !tools.BuiltinServerConnected("mssql") {
		t.Fatal("should be connected")
	}

	if err := tools.DisconnectBuiltinServer("mssql"); err != nil {
		t.Fatalf("DisconnectBuiltinServer: %v", err)
	}

	if tools.BuiltinServerConnected("mssql") {
		t.Error("should not be connected after disconnect")
	}
}

func TestMssqlExecuteQueryRejectsWrite(t *testing.T) {
	// This test does not need a live DB — it should fail at the safety check
	// before attempting to connect.
	fn := mssqlServer().tools["execute_query"].Fn.(ToolFunc)
	_, err := fn(context.Background(), map[string]any{"query": "DELETE FROM users"})
	if err == nil {
		t.Fatal("expected error for DELETE statement")
	}
}

// TestMssqlIntegration runs against a live SQL Server if MSSQL_TEST_LIVE=1.
func TestMssqlIntegration(t *testing.T) {
	if os.Getenv("MSSQL_TEST_LIVE") != "1" {
		t.Skip("set MSSQL_TEST_LIVE=1 and configure SERVER_NAME, DATABASE_NAME, SQL_USERNAME, SQL_PASSWORD to run")
	}

	ctx := context.Background()

	t.Run("list_tables", func(t *testing.T) {
		result, err := mssqlListTables(ctx, nil)
		if err != nil {
			t.Fatalf("list_tables: %v", err)
		}
		t.Log(result)
	})

	t.Run("execute_query", func(t *testing.T) {
		result, err := mssqlExecuteQuery(ctx, map[string]any{"query": "SELECT 1 AS test_col"})
		if err != nil {
			t.Fatalf("execute_query: %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result")
		}
		t.Log(result)
	})
}
