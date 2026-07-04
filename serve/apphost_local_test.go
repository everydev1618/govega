package serve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everydev1618/govega/tools"
)

func TestLocalAppHostStaticLifecycle(t *testing.T) {
	ws := t.TempDir()
	appDir := filepath.Join(ws, "pacman")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	const html = "<!doctype html><canvas id=\"c\"></canvas><script>var x=w/2;</script>"
	if err := os.WriteFile(filepath.Join(appDir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}

	host := NewLocalAppHost(ws)
	host.SetBaseURL("https://tenant.example.com")

	dep, err := host.Deploy(context.Background(), tools.AppSpec{Name: "pacman", Source: "pacman"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if dep.URL != "https://tenant.example.com/apps/pacman/" {
		t.Errorf("URL = %q", dep.URL)
	}
	if dep.Provider != "local" {
		t.Errorf("provider = %q", dep.Provider)
	}

	// The hosted app serves the file byte-for-byte through the /apps/ route.
	srv := httptest.NewServer(host)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/apps/pacman/index.html")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// HTML is served with a <base> tag injected for subpath asset resolution;
	// the original markup is preserved.
	if !strings.Contains(string(body), html) {
		t.Errorf("served content should contain original html.\n got: %q", string(body))
	}
	if !strings.Contains(string(body), `<base href="/apps/pacman/">`) {
		t.Errorf("expected injected <base>, got: %q", string(body))
	}

	// Directory root serves index.html.
	rootResp, err := http.Get(srv.URL + "/apps/pacman/")
	if err != nil {
		t.Fatal(err)
	}
	rootBody, _ := io.ReadAll(rootResp.Body)
	rootResp.Body.Close()
	if !strings.Contains(string(rootBody), html) {
		t.Errorf("root did not serve index.html, got: %q", string(rootBody))
	}

	// List reflects the deployment.
	deps, _ := host.List(context.Background())
	if len(deps) != 1 || deps[0].Name != "pacman" {
		t.Errorf("List = %+v", deps)
	}

	// Destroy removes it; the route then 404s.
	if err := host.Destroy(context.Background(), "pacman"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	gone, _ := http.Get(srv.URL + "/apps/pacman/index.html")
	if gone.StatusCode != 404 {
		t.Errorf("after destroy status = %d, want 404", gone.StatusCode)
	}
	gone.Body.Close()
}

// Deploy must reject a source that escapes the workspace.
func TestLocalAppHostRejectsTraversal(t *testing.T) {
	host := NewLocalAppHost(t.TempDir())
	if _, err := host.Deploy(context.Background(), tools.AppSpec{Name: "x", Source: "../../etc"}); err == nil {
		t.Error("expected traversal source to be rejected")
	}
}

// With a signer wired, the deploy URL carries a ?sig= and the /apps/ route
// gates unsigned requests but lets the signed link (and its cookie'd assets)
// through — and injects <base> so relative assets resolve under the subpath.
func TestLocalAppHostGatingAndBaseInjection(t *testing.T) {
	ws := t.TempDir()
	appDir := filepath.Join(ws, "site")
	os.MkdirAll(appDir, 0755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<head></head><script src=\"app.js\"></script>"), 0644)
	os.WriteFile(filepath.Join(appDir, "app.js"), []byte("// js"), 0644)

	host := NewLocalAppHost(ws)
	host.SetBaseURL("https://tenant.example.com")
	host.SetSigner(newCapabilitySigner("k"))

	dep, err := host.Deploy(context.Background(), tools.AppSpec{Name: "site", Source: "site"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !strings.Contains(dep.URL, "?sig=") {
		t.Fatalf("deploy URL should be signed, got %q", dep.URL)
	}
	sig := dep.URL[strings.Index(dep.URL, "?sig=")+len("?sig="):]

	srv := httptest.NewServer(host)
	defer srv.Close()

	// Unsigned request → 401.
	un, _ := http.Get(srv.URL + "/apps/site/index.html")
	if un.StatusCode != 401 {
		t.Errorf("unsigned request status = %d, want 401", un.StatusCode)
	}
	un.Body.Close()

	// Signed request → 200, <base> injected, and a capability cookie set.
	ok, err := http.Get(srv.URL + "/apps/site/index.html?sig=" + sig)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(ok.Body)
	ok.Body.Close()
	if ok.StatusCode != 200 {
		t.Fatalf("signed request status = %d", ok.StatusCode)
	}
	if !strings.Contains(string(body), `<base href="/apps/site/">`) {
		t.Errorf("expected injected <base>, got: %s", string(body))
	}
	// The capability cookie is scoped to the deliverable. (It's a Secure cookie,
	// which a browser won't send over the test's plain-HTTP server, so forward
	// it manually to exercise the cookie-authorized asset path.)
	var capCookie *http.Cookie
	for _, c := range ok.Cookies() {
		if c.Name == capabilityCookieName {
			capCookie = c
		}
	}
	if capCookie == nil {
		t.Fatal("signed hit should set a capability cookie")
	}

	req, _ := http.NewRequest("GET", srv.URL+"/apps/site/app.js", nil)
	req.AddCookie(capCookie)
	asset, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	asset.Body.Close()
	if asset.StatusCode != 200 {
		t.Errorf("asset via capability cookie status = %d, want 200", asset.StatusCode)
	}
}
