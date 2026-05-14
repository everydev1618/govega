package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// flyMockServer captures every request the sandbox client makes so tests can
// assert against them. It satisfies the subset of the Fly Machines REST API
// the sandbox tools actually call.
type flyMockServer struct {
	*httptest.Server
	calls []recordedCall
}

type recordedCall struct {
	Method string
	Path   string
	Body   map[string]any
}

func newFlyMockServer(t *testing.T) *flyMockServer {
	t.Helper()
	m := &flyMockServer{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recordedCall{Method: r.Method, Path: r.URL.Path}
		if r.Body != nil {
			buf, _ := io.ReadAll(r.Body)
			if len(buf) > 0 {
				_ = json.Unmarshal(buf, &rec.Body)
			}
		}
		m.calls = append(m.calls, rec)

		// Route based on path. Just enough to keep the client happy.
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/apps":
			w.WriteHeader(http.StatusCreated)
		case r.Method == "POST" && r.URL.Path == "/graphql":
			// IP allocation goes through GraphQL — return a synthetic ok
			// payload so spawnApp's error checks pass.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"allocateIpAddress": map[string]any{
						"ipAddress": map[string]any{"address": "::1", "type": "v6"},
					},
				},
			})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/machines"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "machine-abc123"})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/machines"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "machine-abc123"}})
		case strings.HasSuffix(r.URL.Path, "/exec"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "ok",
				"stderr":    "",
				"exit_code": 0,
			})
		case r.Method == "DELETE" && strings.Count(r.URL.Path, "/") == 3:
			// DELETE /v1/apps/{name}
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func TestSandboxSpawnApp(t *testing.T) {
	mock := newFlyMockServer(t)
	c := &flySandboxClient{token: "test", apiBase: mock.URL + "/v1", graphqlBase: mock.URL + "/graphql", org: "vega-apps", image: "registry.fly.io/v39a-sandbox:main", httpClient: http.DefaultClient}

	appID, url, err := c.spawnApp(context.Background(), "todo", 8080)
	if err != nil {
		t.Fatalf("spawnApp: %v", err)
	}
	if !strings.HasPrefix(appID, "todo-") {
		t.Errorf("appID should start with 'todo-' (random suffix), got %q", appID)
	}
	if url != "https://"+appID+".fly.dev" {
		t.Errorf("url mismatch: %q", url)
	}

	// spawnApp must NOT call the Machines REST IP endpoint — that path
	// doesn't exist (returns 404 in production). IP allocation goes
	// through GraphQL instead.
	for _, call := range mock.calls {
		if strings.Contains(call.Path, "/ips/") {
			t.Errorf("spawnApp must not hit Machines /ips endpoint; got %s %s", call.Method, call.Path)
		}
	}

	// Expected sequence: create app, allocate v6, allocate shared_v4,
	// create machine.
	if len(mock.calls) != 4 {
		t.Fatalf("expected exactly 4 API calls, got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0].Method != "POST" || mock.calls[0].Path != "/v1/apps" {
		t.Errorf("first call should create app, got %s %s", mock.calls[0].Method, mock.calls[0].Path)
	}
	if mock.calls[0].Body["org_slug"] != "vega-apps" {
		t.Errorf("app create should be in vega-apps org, got %v", mock.calls[0].Body["org_slug"])
	}

	// Second + third call: GraphQL IP allocations, one v6 and one
	// shared_v4. Order doesn't matter for routing — assert both happen.
	graphqlTypes := map[string]int{}
	for _, call := range mock.calls[1:3] {
		if call.Path != "/graphql" {
			t.Errorf("expected /graphql call, got %s %s", call.Method, call.Path)
			continue
		}
		vars, _ := call.Body["variables"].(map[string]any)
		input, _ := vars["input"].(map[string]any)
		typ, _ := input["type"].(string)
		appIDArg, _ := input["appId"].(string)
		if appIDArg != appID {
			t.Errorf("graphql appId should be %q, got %q", appID, appIDArg)
		}
		graphqlTypes[typ]++
	}
	if graphqlTypes["v6"] != 1 || graphqlTypes["shared_v4"] != 1 {
		t.Errorf("expected one v6 + one shared_v4 allocation, got %v", graphqlTypes)
	}

	// Machine create payload must include the sandbox image and the requested port.
	machineCall := mock.calls[len(mock.calls)-1]
	if machineCall.Method != "POST" || !strings.HasSuffix(machineCall.Path, "/machines") {
		t.Errorf("last call should create machine, got %s %s", machineCall.Method, machineCall.Path)
	}
	cfg, _ := machineCall.Body["config"].(map[string]any)
	if cfg["image"] != "registry.fly.io/v39a-sandbox:main" {
		t.Errorf("image mismatch: %v", cfg["image"])
	}
	services, _ := cfg["services"].([]any)
	if len(services) == 0 {
		t.Fatalf("missing services in machine config")
	}
	svc := services[0].(map[string]any)
	if int(svc["internal_port"].(float64)) != 8080 {
		t.Errorf("internal_port should be 8080, got %v", svc["internal_port"])
	}
}

func TestSandboxRunInApp(t *testing.T) {
	mock := newFlyMockServer(t)
	c := &flySandboxClient{token: "test", apiBase: mock.URL + "/v1", org: "vega-apps", image: "x", httpClient: http.DefaultClient}

	out, err := c.runInApp(context.Background(), "todo-abc123", []string{"ls", "-la"})
	if err != nil {
		t.Fatalf("runInApp: %v", err)
	}
	if out.Stdout != "ok" || out.ExitCode != 0 {
		t.Errorf("unexpected output: %+v", out)
	}
	// Must have: GET machines (to find machine_id), POST exec
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	if !strings.HasSuffix(mock.calls[1].Path, "/exec") {
		t.Errorf("second call should be exec, got %s", mock.calls[1].Path)
	}
	cmd, _ := mock.calls[1].Body["command"].([]any)
	if len(cmd) != 2 || cmd[0] != "ls" || cmd[1] != "-la" {
		t.Errorf("command not passed through: %v", cmd)
	}
}

func TestSandboxWriteFileToApp(t *testing.T) {
	mock := newFlyMockServer(t)
	c := &flySandboxClient{token: "test", apiBase: mock.URL + "/v1", org: "vega-apps", image: "x", httpClient: http.DefaultClient}

	err := c.writeFileToApp(context.Background(), "todo-abc123", "/workspace/app.py", []byte("print('hi')\n"))
	if err != nil {
		t.Fatalf("writeFileToApp: %v", err)
	}
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	cmd, _ := mock.calls[1].Body["command"].([]any)
	if len(cmd) < 3 {
		t.Fatalf("expected sh -c command, got %v", cmd)
	}
	// The base64-encoded content must appear in the third argument (the script).
	script := cmd[2].(string)
	if !strings.Contains(script, "base64") {
		t.Errorf("script should use base64 -d, got: %s", script)
	}
	if !strings.Contains(script, "/workspace/app.py") {
		t.Errorf("script should reference target path: %s", script)
	}
}

func TestSandboxDestroyApp(t *testing.T) {
	mock := newFlyMockServer(t)
	c := &flySandboxClient{token: "test", apiBase: mock.URL + "/v1", org: "vega-apps", image: "x", httpClient: http.DefaultClient}

	err := c.destroyApp(context.Background(), "todo-abc123")
	if err != nil {
		t.Fatalf("destroyApp: %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	if mock.calls[0].Method != "DELETE" || mock.calls[0].Path != "/v1/apps/todo-abc123" {
		t.Errorf("expected DELETE /v1/apps/todo-abc123, got %s %s", mock.calls[0].Method, mock.calls[0].Path)
	}
}

func TestRegisterSandboxToolsRequiresToken(t *testing.T) {
	// Without FLY_SANDBOX_TOKEN, RegisterSandboxTools should be a no-op and
	// the tools should not be present in the Tools collection.
	t.Setenv("FLY_SANDBOX_TOKEN", "")
	tl := NewTools()
	RegisterSandboxTools(tl)
	for _, name := range []string{"spawn_app", "run_in_app", "write_file_to_app", "destroy_app"} {
		if _, err := tl.Execute(context.Background(), name, nil); err == nil {
			t.Errorf("%s should not be registered when FLY_SANDBOX_TOKEN is empty", name)
		}
	}
}

func TestRegisterSandboxToolsWithToken(t *testing.T) {
	t.Setenv("FLY_SANDBOX_TOKEN", "fake-token")
	tl := NewTools()
	RegisterSandboxTools(tl)
	for _, name := range []string{"spawn_app", "run_in_app", "write_file_to_app", "destroy_app"} {
		// Just verify the tool exists. Calling it would hit the real Fly API
		// — that's tested at the client layer above.
		schema := tl.Schema()
		found := false
		for _, s := range schema {
			if s.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s should be registered when FLY_SANDBOX_TOKEN is set", name)
		}
	}
}
