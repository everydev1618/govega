package aire

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"

	aireproto "github.com/aire-protocol/aire-go"
	"github.com/everydev1618/govega/llm"
)

// Server hosts AIRE agents addressable over QUIC. Each registered agent is
// backed by an LLM: incoming INVOKE frames carry a JSON-encoded Request, the
// LLM generates a reply, and the Server sends a single JSON-encoded Reply
// back as a FrameStream.
//
// Server is the natural counterpart to Client: a govega instance acting as
// an AIRE peer hosts a Server (inbound) and uses a Client (outbound).
type Server struct {
	node *aireproto.Node
}

// Request is the JSON-encoded INVOKE payload format expected by a
// Server-hosted LLM agent.
type Request struct {
	Messages []llm.Message `json:"messages"`
}

// Reply is the JSON-encoded response payload a Server-hosted LLM agent
// returns to the caller.
type Reply struct {
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// StreamChunk is the JSON payload of each FrameStream frame returned by an
// LLM agent registered via RegisterLLMAgentStreaming. Either Delta or Error
// is populated per chunk. Stream end is signalled by operation close (FIN),
// not by a sentinel chunk.
type StreamChunk struct {
	Delta string `json:"delta,omitempty"`
	Error string `json:"error,omitempty"`
}

// NewServer creates a Server with the given AIRE node ID. The nodeID SHOULD
// be a DID (per aire-spec §5); did:key is fine for development.
func NewServer(nodeID string) *Server {
	return &Server{
		node: aireproto.NewNode(aireproto.NodeConfig{NodeID: nodeID}),
	}
}

// RegisterLLMAgent registers an agent that runs backend on each inbound
// INVOKE. If systemPrompt is non-empty, it is prepended as a system message
// before the request's messages.
func (s *Server) RegisterLLMAgent(agentID string, backend llm.LLM, systemPrompt string) error {
	if backend == nil {
		return errors.New("aire: RegisterLLMAgent: nil backend")
	}
	return s.node.RegisterAgent(agentID, aireproto.AgentFunc(func(ctx context.Context, inv *aireproto.Invoke) error {
		return handleLLMInvoke(ctx, inv, backend, systemPrompt)
	}))
}

// RegisterLLMAgentStreaming registers an agent that streams backend's
// GenerateStream output as a sequence of FrameStream frames, each carrying
// a JSON-encoded StreamChunk. The operation closes when the LLM stream ends.
func (s *Server) RegisterLLMAgentStreaming(agentID string, backend llm.LLM, systemPrompt string) error {
	if backend == nil {
		return errors.New("aire: RegisterLLMAgentStreaming: nil backend")
	}
	return s.node.RegisterAgent(agentID, aireproto.AgentFunc(func(ctx context.Context, inv *aireproto.Invoke) error {
		return handleLLMInvokeStreaming(ctx, inv, backend, systemPrompt)
	}))
}

// RegisterAgent registers an arbitrary aireproto.Agent under the given ID.
// Use this when the standard request/reply or streaming LLM handlers don't
// fit and the caller wants full control over frame handling.
func (s *Server) RegisterAgent(agentID string, agent aireproto.Agent) error {
	return s.node.RegisterAgent(agentID, agent)
}

// Listen starts accepting QUIC connections on addr. If tlsConf is nil, a
// dev self-signed config is used (NOT FOR PRODUCTION).
func (s *Server) Listen(addr string, tlsConf *tls.Config) error {
	if tlsConf == nil {
		tlsConf = aireproto.DevTLSConfig()
	}
	return s.node.Listen(addr, tlsConf)
}

// Addr returns the bound listener address. Only valid after Listen.
func (s *Server) Addr() string {
	return s.node.Addr()
}

// Stop stops the underlying Node and its accept loop.
func (s *Server) Stop() error {
	return s.node.Stop()
}

func handleLLMInvoke(ctx context.Context, inv *aireproto.Invoke, backend llm.LLM, systemPrompt string) error {
	var req Request
	if len(inv.Args) > 0 {
		if err := json.Unmarshal(inv.Args, &req); err != nil {
			return sendReply(inv.Op, Reply{Error: fmt.Sprintf("decode request: %v", err)})
		}
	}

	messages := req.Messages
	if systemPrompt != "" {
		messages = append([]llm.Message{{Role: llm.RoleSystem, Content: systemPrompt}}, messages...)
	}

	resp, err := backend.Generate(ctx, messages, nil)
	if err != nil {
		return sendReply(inv.Op, Reply{Error: err.Error()})
	}
	return sendReply(inv.Op, Reply{Content: resp.Content})
}

func handleLLMInvokeStreaming(ctx context.Context, inv *aireproto.Invoke, backend llm.LLM, systemPrompt string) error {
	var req Request
	if len(inv.Args) > 0 {
		if err := json.Unmarshal(inv.Args, &req); err != nil {
			return sendChunk(inv.Op, StreamChunk{Error: fmt.Sprintf("decode request: %v", err)})
		}
	}

	messages := req.Messages
	if systemPrompt != "" {
		messages = append([]llm.Message{{Role: llm.RoleSystem, Content: systemPrompt}}, messages...)
	}

	stream, err := backend.GenerateStream(ctx, messages, nil)
	if err != nil {
		return sendChunk(inv.Op, StreamChunk{Error: err.Error()})
	}

	for ev := range stream {
		switch ev.Type {
		case llm.StreamEventContentDelta:
			if ev.Delta == "" {
				continue
			}
			if err := sendChunk(inv.Op, StreamChunk{Delta: ev.Delta}); err != nil {
				return err
			}
		case llm.StreamEventError:
			msg := "unknown error"
			if ev.Error != nil {
				msg = ev.Error.Error()
			}
			return sendChunk(inv.Op, StreamChunk{Error: msg})
		}
	}
	return nil
}

func sendChunk(op *aireproto.Operation, chunk StreamChunk) error {
	payload, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("aire: marshal chunk: %w", err)
	}
	return op.Send(aireproto.Frame{Type: aireproto.FrameStream, Payload: payload})
}

func sendReply(op *aireproto.Operation, reply Reply) error {
	payload, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("aire: marshal reply: %w", err)
	}
	return op.Send(aireproto.Frame{Type: aireproto.FrameStream, Payload: payload})
}
