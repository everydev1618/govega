package serve

import "testing"

// TestMCPServerPersistence exercises the MCP-server persistence surface on
// both backends. These methods lived only on SQLiteStore, gated by type
// assertions in the handlers — on Postgres every one silently no-oped, so
// connected MCP servers vanished on restart.
func TestMCPServerPersistence(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		if err := store.UpsertMCPServer("github", `{"transport":"stdio"}`); err != nil {
			t.Fatalf("UpsertMCPServer: %v", err)
		}
		if err := store.UpsertMCPServer("linear", `{"transport":"http"}`); err != nil {
			t.Fatalf("UpsertMCPServer: %v", err)
		}
		// Upsert overwrites config for an existing name.
		if err := store.UpsertMCPServer("github", `{"transport":"http","url":"x"}`); err != nil {
			t.Fatalf("UpsertMCPServer update: %v", err)
		}

		if err := store.SetMCPServerDisabled("linear", true); err != nil {
			t.Fatalf("SetMCPServerDisabled: %v", err)
		}
		if err := store.SetMCPServerDisabled("nonexistent", true); err == nil {
			t.Error("SetMCPServerDisabled on unknown server should error")
		}

		servers, err := store.ListMCPServers()
		if err != nil {
			t.Fatalf("ListMCPServers: %v", err)
		}
		byName := map[string]MCPServerConfig{}
		for _, sc := range servers {
			byName[sc.Name] = sc
		}
		if len(servers) != 2 {
			t.Fatalf("got %d servers, want 2: %+v", len(servers), servers)
		}
		if byName["github"].ConfigJSON != `{"transport":"http","url":"x"}` {
			t.Errorf("github config = %q (upsert did not overwrite)", byName["github"].ConfigJSON)
		}
		if !byName["linear"].Disabled {
			t.Error("linear should be disabled")
		}
		if byName["github"].Disabled {
			t.Error("github should be enabled")
		}

		if err := store.DeleteMCPServer("github"); err != nil {
			t.Fatalf("DeleteMCPServer: %v", err)
		}
		servers, err = store.ListMCPServers()
		if err != nil {
			t.Fatalf("ListMCPServers after delete: %v", err)
		}
		if len(servers) != 1 || servers[0].Name != "linear" {
			t.Errorf("after delete: %+v, want only linear", servers)
		}
	})
}
