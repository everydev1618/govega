package serve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if string(body) != html {
		t.Errorf("served content mismatch.\n got: %q", string(body))
	}

	// Directory root serves index.html.
	rootResp, err := http.Get(srv.URL + "/apps/pacman/")
	if err != nil {
		t.Fatal(err)
	}
	rootBody, _ := io.ReadAll(rootResp.Body)
	rootResp.Body.Close()
	if string(rootBody) != html {
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
