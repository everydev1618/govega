// Sandbox tools let the agent spawn isolated Fly machines to run user-built
// apps. Each call to spawn_app creates a fresh Fly app + machine in the
// "vega-apps" org using the v39a-sandbox image; write_file_to_app and
// run_in_app drive it via Fly's Machines /exec endpoint.
//
// The tools are only registered if FLY_SANDBOX_TOKEN is present in the
// environment (v39a injects it at instance launch when the customer is
// permitted to spawn sandboxes). Without the token, no tools are registered
// — the agent simply doesn't see them.
package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

const (
	defaultFlyAPIBase     = "https://api.machines.dev/v1"
	defaultFlyGraphQLBase = "https://api.fly.io/graphql"
	defaultSandboxOrg     = "vega-apps"
	defaultSandboxImg     = "registry.fly.io/v39a-sandbox:main"
	defaultRegion         = "iad"
	httpTimeoutSeconds    = 60
)

// flySandboxClient is a small REST client for the Fly Machines API, scoped
// to the org that hosts sandbox apps. IP allocation goes through Fly's
// GraphQL endpoint (graphqlBase) rather than the Machines API, which has
// no IP-allocation resource.
type flySandboxClient struct {
	token       string
	apiBase     string
	graphqlBase string
	org         string
	image       string
	region      string
	httpClient  *http.Client
}

func newFlySandboxClient() (*flySandboxClient, error) {
	token := os.Getenv("FLY_SANDBOX_TOKEN")
	if token == "" {
		return nil, errors.New("FLY_SANDBOX_TOKEN is not set")
	}
	return &flySandboxClient{
		token:       token,
		apiBase:     envOr("FLY_API_BASE", defaultFlyAPIBase),
		graphqlBase: envOr("FLY_GRAPHQL_BASE", defaultFlyGraphQLBase),
		org:         envOr("FLY_SANDBOX_ORG", defaultSandboxOrg),
		image:       envOr("FLY_SANDBOX_IMAGE", defaultSandboxImg),
		region:      envOr("FLY_SANDBOX_REGION", defaultRegion),
		httpClient:  &http.Client{Timeout: time.Duration(httpTimeoutSeconds) * time.Second},
	}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// call wraps an HTTP request/response cycle, handling JSON encode/decode and
// turning non-2xx responses into Go errors with the response body included.
func (c *flySandboxClient) call(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("fly api %s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
		}
	}
	return nil
}

// randomSuffix returns 6 hex chars. Used to disambiguate app names so two
// agents can both spawn an app called "todo" without colliding.
func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// allocateIP attaches a public IP of the given type to the app via Fly's
// GraphQL API. ipType is one of: "v6" (dedicated public IPv6), "v4"
// (dedicated public IPv4 — costs $), "shared_v4" (anycast IPv4, free).
// Returns the allocated address, which may be empty for shared_v4 (the
// shared pool has no per-app address).
//
// We hit GraphQL rather than REST because the Fly Machines API has no
// IP-allocation resource. The earlier implementation here POST'd to
// /apps/{name}/ips/allocate-v4 on the Machines API, which returns a
// 404 page for every call — that path only exists conceptually inside
// flyctl, which translates it to this GraphQL mutation. Without IP
// allocation the spawned app has no public IPs and *.fly.dev DNS
// returns NXDOMAIN, so the dashboard URL the agent prints is dead.
func (c *flySandboxClient) allocateIP(ctx context.Context, appName, ipType string) error {
	body := map[string]any{
		"query": `mutation($input: AllocateIPAddressInput!) { allocateIpAddress(input: $input) { ipAddress { address type } } }`,
		"variables": map[string]any{
			"input": map[string]any{
				"appId": appName,
				"type":  ipType,
			},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal graphql body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.graphqlBase, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("fly graphql HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	// GraphQL returns HTTP 200 even on logical errors — the errors[] array
	// is the real failure signal.
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("decode graphql response: %w (body: %s)", err, string(raw))
	}
	if len(out.Errors) > 0 {
		return fmt.Errorf("fly graphql: %s", out.Errors[0].Message)
	}
	return nil
}

// spawnApp creates a Fly app + machine running the sandbox image. Returns the
// (unique) app id and the public URL the user can visit.
//
// Any failure after the app exists tears it back down (best-effort) —
// otherwise each failed spawn leaks a machineless "pending" app into the
// sandbox org, one per agent retry.
func (c *flySandboxClient) spawnApp(ctx context.Context, name string, port int) (_ string, _ string, err error) {
	if port <= 0 {
		port = 8080
	}
	appID := name + "-" + randomSuffix()

	if err := c.call(ctx, "POST", "/apps", map[string]any{
		"app_name": appID,
		"org_slug": c.org,
	}, nil); err != nil {
		return "", "", fmt.Errorf("create app: %w", err)
	}
	defer func() {
		if err != nil {
			// context.WithoutCancel: still clean up when the failure was a
			// cancelled/expired ctx.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_ = c.destroyApp(cleanupCtx, appID)
		}
	}()

	// IP allocation happens via Fly's GraphQL API — the Machines API has
	// no IP resource. Without these two calls the app has no public IPs
	// and *.fly.dev won't resolve, leaving the dashboard unreachable
	// even though the machine itself runs fine. v6 is a dedicated
	// public IPv6; shared_v4 opts the app into Fly's anycast IPv4 so
	// IPv4-only clients can reach it too.
	if err := c.allocateIP(ctx, appID, "v6"); err != nil {
		return "", "", fmt.Errorf("allocate v6: %w", err)
	}
	if err := c.allocateIP(ctx, appID, "shared_v4"); err != nil {
		return "", "", fmt.Errorf("allocate shared_v4: %w", err)
	}

	machineConfig := map[string]any{
		"region": c.region,
		"config": map[string]any{
			"image": c.image,
			"services": []map[string]any{{
				"internal_port": port,
				"protocol":      "tcp",
				"ports": []map[string]any{
					{"port": 443, "handlers": []string{"tls", "http"}},
					{"port": 80, "handlers": []string{"http"}},
				},
			}},
			"guest": map[string]any{
				"cpu_kind":  "shared",
				"cpus":      1,
				"memory_mb": 256,
			},
		},
	}
	if err := c.call(ctx, "POST", "/apps/"+appID+"/machines", machineConfig, nil); err != nil {
		return "", "", fmt.Errorf("create machine: %w", err)
	}

	return appID, "https://" + appID + ".fly.dev", nil
}

// findMachineID returns the id of the (single) machine in a sandbox app.
func (c *flySandboxClient) findMachineID(ctx context.Context, appID string) (string, error) {
	var machines []struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, "GET", "/apps/"+appID+"/machines", nil, &machines); err != nil {
		return "", fmt.Errorf("list machines: %w", err)
	}
	if len(machines) == 0 {
		return "", fmt.Errorf("no machines in app %s", appID)
	}
	return machines[0].ID, nil
}

// ExecResult is the parsed shape of a Fly Machines /exec response.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// runInApp executes a one-shot command in the sandbox via Fly's exec endpoint.
func (c *flySandboxClient) runInApp(ctx context.Context, appID string, cmd []string) (*ExecResult, error) {
	machineID, err := c.findMachineID(ctx, appID)
	if err != nil {
		return nil, err
	}
	var out ExecResult
	if err := c.call(ctx, "POST", "/apps/"+appID+"/machines/"+machineID+"/exec", map[string]any{
		"command": cmd,
		"timeout": 60,
	}, &out); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return &out, nil
}

// writeFileToApp writes content to the given path inside the sandbox by
// base64-encoding it into a shell command, since Fly's /exec doesn't take
// stdin. mkdir -p ensures the parent directory exists.
func (c *flySandboxClient) writeFileToApp(ctx context.Context, appID, filepath string, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	// Shell-escape filepath to be safe.
	escaped := strings.ReplaceAll(filepath, "'", `'\''`)
	dir := path.Dir(escaped)
	script := fmt.Sprintf("mkdir -p '%s' && echo '%s' | base64 -d > '%s'", dir, encoded, escaped)

	res, err := c.runInApp(ctx, appID, []string{"sh", "-c", script})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("write_file_to_app exited %d: %s", res.ExitCode, res.Stderr)
	}
	return nil
}

// destroyApp tears down the Fly app, cascading to its machine + volumes.
func (c *flySandboxClient) destroyApp(ctx context.Context, appID string) error {
	return c.call(ctx, "DELETE", "/apps/"+appID, nil, nil)
}

// SandboxToolNames is the canonical list of tools RegisterSandboxTools adds
// when FLY_SANDBOX_TOKEN is set. The DSL layer uses this so longstanding
// agents (whose persisted Tools allow-list predates the sandbox surface)
// still receive the sandbox tools when the host supports them.
func SandboxToolNames() []string {
	return []string{"spawn_app", "run_in_app", "write_file_to_app", "destroy_app"}
}

// RegisterSandboxTools wires the four sandbox tools into the Tools collection
// if FLY_SANDBOX_TOKEN is set. If the token is absent, this is a no-op so the
// tool surface for agents that don't have sandbox permission stays clean.
func RegisterSandboxTools(t *Tools) {
	c, err := newFlySandboxClient()
	if err != nil {
		// No token → not authorized to spawn sandboxes → no tools registered.
		return
	}

	t.Register("spawn_app", ToolDef{
		Description: "Spawn a new isolated sandbox machine to host a user-built app. " +
			"THIS is how you produce a URL the user can visit — never serve from inside " +
			"your own container (that URL would be localhost-only and unreachable). " +
			"Use whenever you build, deploy, preview, or share anything the user opens " +
			"in a browser: dashboards, static HTML, web apps, demos. The sandbox comes " +
			"with node, python, go, git, and common build tools preinstalled. Returns " +
			"the app id (use with other sandbox tools) and the public *.fly.dev URL.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			port := 8080
			if v, ok := params["port"].(float64); ok {
				port = int(v)
			}
			if name == "" {
				return "", errors.New("name is required")
			}
			appID, url, err := c.spawnApp(ctx, name, port)
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(map[string]any{"app_id": appID, "url": url})
			return string(out), nil
		},
		Params: map[string]ParamDef{
			"name": {Type: "string", Description: "Short friendly name for the app, e.g. 'todo' or 'blog'. A random suffix is appended to avoid collisions.", Required: true},
			"port": {Type: "integer", Description: "Port the app will listen on inside the container. Defaults to 8080.", Required: false},
		},
	})

	t.Register("run_in_app", ToolDef{
		Description: "Run a one-shot shell command inside a sandbox app and return its output. " +
			"Use this to install dependencies (npm install, pip install), start the user's " +
			"app as a background process (nohup ... &), check status, or read logs. The " +
			"working directory is /workspace.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			appID, _ := params["app_id"].(string)
			cmdAny, ok := params["command"].([]any)
			if appID == "" {
				return "", errors.New("app_id is required")
			}
			if !ok || len(cmdAny) == 0 {
				return "", errors.New("command must be a non-empty array of strings")
			}
			cmd := make([]string, len(cmdAny))
			for i, c := range cmdAny {
				cmd[i], _ = c.(string)
			}
			res, err := c.runInApp(ctx, appID, cmd)
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(res)
			return string(out), nil
		},
		Params: map[string]ParamDef{
			"app_id":  {Type: "string", Description: "The app id returned by spawn_app", Required: true},
			"command": {Type: "array", Description: "Command + args as a string array, e.g. ['sh', '-c', 'cd /workspace && python app.py']", Required: true},
		},
	})

	t.Register("write_file_to_app", ToolDef{
		Description: "Write a file inside a sandbox app. Use this to deliver user code (one " +
			"file per call). Parent directories are created automatically. Files larger " +
			"than ~5MB will fail — split big blobs across multiple calls.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			appID, _ := params["app_id"].(string)
			fpath, _ := params["path"].(string)
			content, _ := params["content"].(string)
			if appID == "" || fpath == "" {
				return "", errors.New("app_id and path are required")
			}
			if err := c.writeFileToApp(ctx, appID, fpath, []byte(content)); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(content), fpath), nil
		},
		Params: map[string]ParamDef{
			"app_id":  {Type: "string", Description: "The app id returned by spawn_app", Required: true},
			"path":    {Type: "string", Description: "Absolute path inside the sandbox, e.g. /workspace/app.py", Required: true},
			"content": {Type: "string", Description: "File contents (text). Will be base64-transferred under the hood.", Required: true},
		},
	})

	t.Register("destroy_app", ToolDef{
		Description: "Permanently destroy a sandbox app and its machine. Use this when the " +
			"user is done with an app or asks you to clean up. Irreversible.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			appID, _ := params["app_id"].(string)
			if appID == "" {
				return "", errors.New("app_id is required")
			}
			if err := c.destroyApp(ctx, appID); err != nil {
				return "", err
			}
			return "destroyed " + appID, nil
		},
		Params: map[string]ParamDef{
			"app_id": {Type: "string", Description: "The app id returned by spawn_app", Required: true},
		},
	})
}
