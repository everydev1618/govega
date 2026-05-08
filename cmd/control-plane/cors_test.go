package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsTestServer(t *testing.T, allowed map[string]bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /dev/mint", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(corsMiddleware(allowed)(mux))
	t.Cleanup(srv.Close)
	return srv
}

func TestControlPlaneCORS_PreflightAllowedOrigin(t *testing.T) {
	srv := corsTestServer(t, map[string]bool{"http://localhost:5173": true})
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/dev/mint", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Dev-Secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status=%d, want 204", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("ACAO=%q, want \"http://localhost:5173\"", got)
	}
	if !contains(res.Header.Get("Access-Control-Allow-Headers"), "X-Dev-Secret") {
		t.Errorf("Allow-Headers missing X-Dev-Secret: %q", res.Header.Get("Access-Control-Allow-Headers"))
	}
}

func TestControlPlaneCORS_PreflightDisallowedOrigin(t *testing.T) {
	srv := corsTestServer(t, map[string]bool{"http://localhost:5173": true})
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/dev/mint", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("ACAO leaked to disallowed origin")
	}
}

func TestControlPlaneCORS_AllowedPostEchoesOrigin(t *testing.T) {
	srv := corsTestServer(t, map[string]bool{"http://localhost:5173": true})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/dev/mint", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("ACAO not echoed on POST")
	}
}

func TestLoadCPCORSConfig_EnvParsing(t *testing.T) {
	cases := []struct {
		env  string
		want map[string]bool
	}{
		{"", nil},
		{"http://localhost:5173", map[string]bool{"http://localhost:5173": true}},
		{"http://localhost:5173, https://app.apex.io", map[string]bool{
			"http://localhost:5173": true,
			"https://app.apex.io":   true,
		}},
	}
	for _, tc := range cases {
		t.Setenv("CONTROL_PLANE_ALLOWED_ORIGINS", tc.env)
		got := loadCORSConfig()
		if !mapEq(got, tc.want) {
			t.Errorf("env=%q got=%v want=%v", tc.env, got, tc.want)
		}
	}
}

// helpers

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func mapEq(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
