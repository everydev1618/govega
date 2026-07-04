package serve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/everydev1618/govega/tools"
)

// LocalAppHost is the vendor-neutral default AppHost: it hosts apps on the Vega
// machine itself and reverse-proxies them under /apps/<name>/ on the Vega
// server. Because it rides the server the user already reaches, it works with
// zero external vendor for local dev, self-hosted-remote, and (routed through
// the edge) v39a. Static apps are served straight from the workspace; dynamic
// apps run as a subprocess and are proxied.
//
// See docs/app-hosting-design.md. Visibility enforcement (capability tokens) is
// layered on separately (Phase 2 of #116); this type handles hosting + routing.
type LocalAppHost struct {
	workspace string
	mu        sync.Mutex
	baseURL   string
	signer    *capabilitySigner    // nil ⇒ open mode (no gating, unsigned URLs)
	deps      map[string]*localDep // keyed by app name
}

type localDep struct {
	name       string
	dir        string // absolute source dir under the workspace
	static     bool
	port       int       // dynamic apps only
	cmd        *exec.Cmd // dynamic apps only
	visibility tools.Visibility
}

// NewLocalAppHost roots the host at the given workspace directory.
func NewLocalAppHost(workspace string) *LocalAppHost {
	return &LocalAppHost{workspace: workspace, deps: make(map[string]*localDep)}
}

// SetBaseURL records the externally-reachable base URL used to build app URLs.
// Called once the listener has resolved its address.
func (h *LocalAppHost) SetBaseURL(u string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.baseURL = strings.TrimRight(u, "/")
}

// SetSigner wires the capability-token signer. When set, app URLs are signed
// and the /apps/ route is gated; nil keeps open mode.
func (h *LocalAppHost) SetSigner(s *capabilitySigner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.signer = s
}

func (h *LocalAppHost) urlFor(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.urlForLocked(name)
}

// urlForLocked builds the app URL, signed with a capability token when a signer
// is wired. Caller must hold h.mu.
func (h *LocalAppHost) urlForLocked(name string) string {
	path := "/apps/" + name + "/"
	url := h.baseURL + path
	if h.signer != nil {
		url += "?sig=" + h.signer.SignURLPath(path)
	}
	return url
}

// Deploy hosts an app. Empty spec.Command ⇒ static file serving from the source
// dir; otherwise the command is started as a subprocess and proxied.
func (h *LocalAppHost) Deploy(ctx context.Context, spec tools.AppSpec) (tools.AppDeployment, error) {
	if spec.Name == "" || spec.Source == "" {
		return tools.AppDeployment{}, fmt.Errorf("name and source are required")
	}
	dir, err := h.safeDir(spec.Source)
	if err != nil {
		return tools.AppDeployment{}, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return tools.AppDeployment{}, fmt.Errorf("source %q is not a directory in the workspace", spec.Source)
	}

	dep := &localDep{name: spec.Name, dir: dir, static: len(spec.Command) == 0, visibility: spec.Visibility}

	if !dep.static {
		port := spec.Port
		if port <= 0 {
			port, err = freePort()
			if err != nil {
				return tools.AppDeployment{}, fmt.Errorf("allocate port: %w", err)
			}
		}
		cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
		if err := cmd.Start(); err != nil {
			return tools.AppDeployment{}, fmt.Errorf("start app: %w", err)
		}
		dep.port = port
		dep.cmd = cmd
	}

	h.mu.Lock()
	if existing, ok := h.deps[spec.Name]; ok {
		h.stopLocked(existing)
	}
	h.deps[spec.Name] = dep
	h.mu.Unlock()

	return tools.AppDeployment{ID: spec.Name, Name: spec.Name, URL: h.urlFor(spec.Name), Provider: "local"}, nil
}

// Destroy stops and removes a deployment.
func (h *LocalAppHost) Destroy(ctx context.Context, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	dep, ok := h.deps[id]
	if !ok {
		return fmt.Errorf("no deployment %q", id)
	}
	h.stopLocked(dep)
	delete(h.deps, id)
	return nil
}

// List returns the active deployments.
func (h *LocalAppHost) List(ctx context.Context) ([]tools.AppDeployment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]tools.AppDeployment, 0, len(h.deps))
	for name := range h.deps {
		out = append(out, tools.AppDeployment{ID: name, Name: name, URL: h.urlForLocked(name), Provider: "local"})
	}
	return out, nil
}

// stopLocked kills a dynamic app's subprocess. Caller holds h.mu.
func (h *LocalAppHost) stopLocked(dep *localDep) {
	if dep.cmd != nil && dep.cmd.Process != nil {
		_ = dep.cmd.Process.Kill()
	}
}

// ServeHTTP dispatches /apps/<name>/… to the matching deployment: a file server
// for static apps, a reverse proxy for dynamic ones.
func (h *LocalAppHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/apps/")
	name, _, _ := strings.Cut(rest, "/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	dep := h.deps[name]
	signer := h.signer
	h.mu.Unlock()
	if dep == nil {
		http.NotFound(w, r)
		return
	}

	// Capability gate: a signed link or portal session is required when gating
	// is on; open mode always allows.
	allow, cookie := capabilityGate(signer, r)
	if !allow {
		http.Error(w, "unauthorized — this app link is missing its access token", http.StatusUnauthorized)
		return
	}
	if cookie != nil {
		http.SetCookie(w, cookie)
	}

	prefix := "/apps/" + name + "/"
	if dep.static {
		serveStaticWithBase(w, r, dep.dir, prefix)
		return
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", dep.port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	http.StripPrefix(strings.TrimRight(prefix, "/"), proxy).ServeHTTP(w, r)
}

// serveStaticWithBase serves files from a static app's directory. For HTML it
// injects <base href="<prefix>"> so relative asset references resolve under the
// subpath (et.v39a.com/apps/<name>/) even when the author wrote bare relative
// paths. Non-HTML is served verbatim by the standard file server.
func serveStaticWithBase(w http.ResponseWriter, r *http.Request, dir, prefix string) {
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	target := filepath.Join(dir, filepath.Clean("/"+rel))
	if rel == "" || strings.HasSuffix(rel, "/") {
		target = filepath.Join(target, "index.html")
	}
	if strings.HasSuffix(target, ".html") {
		if data, err := os.ReadFile(target); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(injectBaseTag(data, prefix))
			return
		}
	}
	http.StripPrefix(prefix, http.FileServer(http.Dir(dir))).ServeHTTP(w, r)
}

// injectBaseTag inserts <base href="prefix"> right after the first <head> tag
// (or prepends it when there's no head), so relative URLs resolve under prefix.
// Idempotent-ish: skips injection if a <base is already present.
func injectBaseTag(html []byte, prefix string) []byte {
	lower := strings.ToLower(string(html))
	if strings.Contains(lower, "<base") {
		return html
	}
	tag := fmt.Sprintf(`<base href="%s">`, prefix)
	if i := strings.Index(lower, "<head>"); i >= 0 {
		at := i + len("<head>")
		return []byte(string(html[:at]) + tag + string(html[at:]))
	}
	return append([]byte(tag), html...)
}

// safeDir resolves a workspace-relative source dir, rejecting traversal.
func (h *LocalAppHost) safeDir(source string) (string, error) {
	cleaned := filepath.Clean(filepath.Join(h.workspace, source))
	if !strings.HasPrefix(cleaned, filepath.Clean(h.workspace)) {
		return "", fmt.Errorf("source escapes the workspace")
	}
	return cleaned, nil
}

// freePort asks the OS for an unused TCP port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
