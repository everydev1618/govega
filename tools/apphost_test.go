package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeAppHost records the last spec it was asked to deploy.
type fakeAppHost struct {
	lastSpec AppSpec
	deps     []AppDeployment
}

func (f *fakeAppHost) Deploy(_ context.Context, spec AppSpec) (AppDeployment, error) {
	f.lastSpec = spec
	dep := AppDeployment{ID: "dep-1", Name: spec.Name, URL: "https://host/apps/" + spec.Name + "/", Provider: "fake"}
	f.deps = append(f.deps, dep)
	return dep, nil
}
func (f *fakeAppHost) Destroy(_ context.Context, id string) error      { return nil }
func (f *fakeAppHost) List(_ context.Context) ([]AppDeployment, error) { return f.deps, nil }

func TestDeployAppDelegatesToHost(t *testing.T) {
	ts := NewTools()
	ts.RegisterBuiltins()
	host := &fakeAppHost{}
	ts.SetAppHost(host)

	out, err := ts.Execute(context.Background(), "deploy_app", map[string]any{
		"name":       "pacman",
		"source":     "pacman",
		"visibility": "public",
	})
	if err != nil {
		t.Fatalf("deploy_app: %v", err)
	}
	if host.lastSpec.Name != "pacman" || host.lastSpec.Source != "pacman" {
		t.Errorf("host received wrong spec: %+v", host.lastSpec)
	}
	if host.lastSpec.Visibility != VisibilityPublic {
		t.Errorf("visibility not passed through: %q", host.lastSpec.Visibility)
	}
	var dep AppDeployment
	if err := json.Unmarshal([]byte(out), &dep); err != nil {
		t.Fatalf("result not JSON AppDeployment: %v (%s)", err, out)
	}
	if !strings.Contains(dep.URL, "pacman") {
		t.Errorf("deployment URL missing: %q", dep.URL)
	}
}

// deploy_app must default Visibility to portal_gated — a hosted app is not
// world-readable unless the agent explicitly asks.
func TestDeployAppDefaultsToPortalGated(t *testing.T) {
	ts := NewTools()
	ts.RegisterBuiltins()
	host := &fakeAppHost{}
	ts.SetAppHost(host)

	if _, err := ts.Execute(context.Background(), "deploy_app", map[string]any{
		"name": "site", "source": "site",
	}); err != nil {
		t.Fatalf("deploy_app: %v", err)
	}
	if host.lastSpec.Visibility != VisibilityPortalGated {
		t.Errorf("default visibility = %q, want portal_gated", host.lastSpec.Visibility)
	}
}

// Without a wired host, deploy_app must fail with a clear message rather than
// pretend to succeed.
func TestDeployAppErrorsWithoutHost(t *testing.T) {
	ts := NewTools()
	ts.RegisterBuiltins()

	_, err := ts.Execute(context.Background(), "deploy_app", map[string]any{
		"name": "x", "source": "x",
	})
	if err == nil {
		t.Fatal("expected an error when no app host is configured")
	}
}
