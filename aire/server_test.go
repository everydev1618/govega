package aire_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	aireproto "github.com/aire-protocol/aire-go"
	vaire "github.com/everydev1618/govega/aire"
	"github.com/everydev1618/govega/llm"
)

type stubLLM struct {
	response   string
	err        error
	seenSystem string
	seenUser   string
}

func (s *stubLLM) Generate(_ context.Context, messages []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem:
			s.seenSystem = m.Content
		case llm.RoleUser:
			s.seenUser = m.Content
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return &llm.LLMResponse{Content: s.response}, nil
}

func (s *stubLLM) GenerateStream(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("not implemented")
}

func TestServer_LLMAgent_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	backend := &stubLLM{response: "Paris"}
	server := vaire.NewServer(nil)
	if err := server.RegisterLLMAgent("iris", backend, "You are a geography expert."); err != nil {
		t.Fatalf("RegisterLLMAgent: %v", err)
	}
	if err := server.Listen("127.0.0.1:0", nil); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = server.Stop() }()

	client := vaire.NewClient()
	client.TLSConfig = aireproto.DevTLSConfig()
	client.Resolve = func(_ context.Context, _ string) (*aireproto.Address, error) {
		return &aireproto.Address{Endpoint: server.Addr(), AgentID: "iris"}, nil
	}
	defer func() { _ = client.Close() }()

	req := vaire.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "capital of france?"}}}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	raw, err := client.Send(ctx, "iris@test", "", "chat", payload)
	if err != nil {
		t.Fatalf("client.Send: %v", err)
	}

	var reply vaire.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if reply.Error != "" {
		t.Fatalf("unexpected Reply.Error: %s", reply.Error)
	}
	if reply.Content != "Paris" {
		t.Errorf("Reply.Content = %q, want Paris", reply.Content)
	}
	if backend.seenSystem != "You are a geography expert." {
		t.Errorf("backend.seenSystem = %q, want system prompt to be prepended", backend.seenSystem)
	}
	if backend.seenUser != "capital of france?" {
		t.Errorf("backend.seenUser = %q, want user message to pass through", backend.seenUser)
	}
}

func TestServer_LLMAgent_BackendError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := vaire.NewServer(nil)
	if err := server.RegisterLLMAgent("iris", &stubLLM{err: errors.New("model is down")}, ""); err != nil {
		t.Fatalf("RegisterLLMAgent: %v", err)
	}
	if err := server.Listen("127.0.0.1:0", nil); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = server.Stop() }()

	client := vaire.NewClient()
	client.TLSConfig = aireproto.DevTLSConfig()
	client.Resolve = func(_ context.Context, _ string) (*aireproto.Address, error) {
		return &aireproto.Address{Endpoint: server.Addr(), AgentID: "iris"}, nil
	}
	defer func() { _ = client.Close() }()

	req := vaire.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "anything"}}}
	payload, _ := json.Marshal(req)
	raw, err := client.Send(ctx, "iris", "", "chat", payload)
	if err != nil {
		t.Fatalf("client.Send: %v", err)
	}
	var reply vaire.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if reply.Error == "" {
		t.Errorf("expected Reply.Error to be populated, got empty")
	}
	if reply.Content != "" {
		t.Errorf("expected empty Reply.Content on error, got %q", reply.Content)
	}
}

func TestServer_RegisterLLMAgent_NilBackend(t *testing.T) {
	server := vaire.NewServer(nil)
	if err := server.RegisterLLMAgent("x", nil, ""); err == nil {
		t.Error("expected error registering with nil backend")
	}
}
