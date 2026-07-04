package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// AppHost hosts an agent-built app and returns a reachable URL. Implementations
// range from an in-process subprocess+reverse-proxy (the vendor-neutral default,
// serve.LocalAppHost) to an isolated cloud machine (a FlyAppHost provider). The
// agent that calls deploy_app never learns which — that keeps the agent surface
// free of vendor lock-in. Providers are wired by the host via SetAppHost.
//
// See docs/app-hosting-design.md.
type AppHost interface {
	// Deploy makes an app reachable and returns its URL + a teardown handle.
	Deploy(ctx context.Context, spec AppSpec) (AppDeployment, error)
	// Destroy tears down a deployment by id.
	Destroy(ctx context.Context, id string) error
	// List returns the currently-hosted deployments.
	List(ctx context.Context) ([]AppDeployment, error)
}

// Visibility controls who can reach a deployment's URL.
type Visibility string

const (
	// VisibilityPortalGated (default) requires a portal session or a
	// capability token scoped to the app — the safe default for a
	// multi-tenant host.
	VisibilityPortalGated Visibility = "portal_gated"
	// VisibilityPublic is reachable by anyone with the link.
	VisibilityPublic Visibility = "public"
	// VisibilityPrivate requires an authenticated portal session.
	VisibilityPrivate Visibility = "private"
)

// DataAccess controls whether the app can reach the tenant's data.
type DataAccess string

const (
	// DataAccessNone (default) — the app is fully isolated from tenant data.
	DataAccessNone DataAccess = "none"
	// DataAccessTenantAPI — the app may call the tenant API with injected
	// identity (the App Contract). No shared filesystem.
	DataAccessTenantAPI DataAccess = "tenant_api"
)

// AppSpec describes an app to host.
type AppSpec struct {
	Name       string     `json:"name"`
	Source     string     `json:"source"`  // workspace-relative directory
	Command    []string   `json:"command"` // empty ⇒ static file serving
	Port       int        `json:"port"`    // internal port for dynamic apps
	Visibility Visibility `json:"visibility"`
	DataAccess DataAccess `json:"data_access"`
}

// AppDeployment is a hosted app.
type AppDeployment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider"`
}

// DeployToolNames lists the app-hosting tools. Meta-agents (orchestrator,
// builder) are denied these alongside the shell/build tools — a router
// dispatches build+deploy work to specialists, it doesn't host apps itself.
func DeployToolNames() []string {
	return []string{"deploy_app", "list_deployments", "destroy_deployment"}
}

// SetAppHost wires the app-hosting provider. Called by the host (serve wires
// LocalAppHost by default; v39a wires a FlyAppHost). nil ⇒ deploy_app reports
// that hosting isn't configured.
func (t *Tools) SetAppHost(h AppHost) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appHost = h
}

// errNoAppHost is returned by the deploy tools when no provider is wired.
var errNoAppHost = errors.New("app hosting is not configured on this Vega instance")

// registerAppTools adds the provider-agnostic app-hosting tools. They delegate
// to the wired AppHost, so the agent surface is identical regardless of backend.
func (t *Tools) registerAppTools() {
	t.Register("deploy_app", ToolDef{
		Description: "Host an app you built so the user can open it at a URL. Point this " +
			"at a directory in your workspace. For a static site (HTML/CSS/JS, canvas " +
			"games) leave 'command' empty. For a dynamic app (Node, Python, Go) give the " +
			"'command' that starts the server and the 'port' it listens on. Returns the " +
			"public URL — always report it to the user. This is how you deliver a running " +
			"app; never bind a localhost port yourself (unreachable). IMPORTANT: the app " +
			"is served under a subpath (…/apps/<name>/), so reference assets with RELATIVE " +
			"paths ('app.js', './style.css'), never root-absolute paths ('/app.js') — the " +
			"latter break under the subpath.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			host := t.appHost
			if host == nil {
				return "", errNoAppHost
			}
			name, err := requireString(params, "name")
			if err != nil {
				return "", err
			}
			source, err := requireString(params, "source")
			if err != nil {
				return "", err
			}
			spec := AppSpec{
				Name:       name,
				Source:     source,
				Visibility: VisibilityPortalGated,
				DataAccess: DataAccessNone,
			}
			if cmd, ok := params["command"].([]any); ok {
				for _, c := range cmd {
					if s, ok := c.(string); ok {
						spec.Command = append(spec.Command, s)
					}
				}
			}
			if v, ok := params["port"].(float64); ok {
				spec.Port = int(v)
			}
			if v, ok := params["visibility"].(string); ok && v != "" {
				spec.Visibility = Visibility(v)
			}
			if v, ok := params["data_access"].(string); ok && v != "" {
				spec.DataAccess = DataAccess(v)
			}
			dep, err := host.Deploy(ctx, spec)
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(dep)
			return string(out), nil
		},
		Params: map[string]ParamDef{
			"name":        {Type: "string", Description: "Short friendly name, e.g. 'pacman'.", Required: true},
			"source":      {Type: "string", Description: "Workspace-relative directory the app lives in, e.g. 'pacman'.", Required: true},
			"command":     {Type: "array", Description: "Command to start a dynamic server, e.g. ['python','app.py']. Omit for a static site.", Required: false},
			"port":        {Type: "integer", Description: "Port the dynamic server listens on. Omit for a static site.", Required: false},
			"visibility":  {Type: "string", Description: "'portal_gated' (default), 'public', or 'private'.", Required: false},
			"data_access": {Type: "string", Description: "'none' (default) or 'tenant_api' to let the app call the tenant API.", Required: false},
		},
	})

	t.Register("list_deployments", ToolDef{
		Description: "List the apps currently hosted for this user.",
		Fn: func(ctx context.Context, _ map[string]any) (string, error) {
			host := t.appHost
			if host == nil {
				return "", errNoAppHost
			}
			deps, err := host.List(ctx)
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(deps)
			return string(out), nil
		},
		Params: map[string]ParamDef{},
	})

	t.Register("destroy_deployment", ToolDef{
		Description: "Tear down a hosted app by its id (from deploy_app/list_deployments).",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			host := t.appHost
			if host == nil {
				return "", errNoAppHost
			}
			id, err := requireString(params, "id")
			if err != nil {
				return "", err
			}
			if err := host.Destroy(ctx, id); err != nil {
				return "", err
			}
			return fmt.Sprintf("destroyed deployment %s", id), nil
		},
		Params: map[string]ParamDef{
			"id": {Type: "string", Description: "Deployment id returned by deploy_app.", Required: true},
		},
	})
}
