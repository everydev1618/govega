package serve

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	aire "github.com/aire-protocol/aire-go"
	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/serve/peering"
)

// peeringStoreProvider is the optional interface the concrete store impls
// satisfy to expose a peering.Store view of the same underlying database.
// Implemented by SQLiteStore + PostgresStore; the Store interface itself
// stays uncluttered.
type peeringStoreProvider interface {
	PeeringStore() peering.Store
}

// startPeering opens the AIRE listener (if VEGA_PEERING_ADDR is set) and
// wires a Dialer for outbound invokes. Idempotent: if the env var is unset
// or the store doesn't support peering, it logs and returns without error.
// Errors during startup are logged at error level; the rest of the server
// boots regardless — peering is a non-fatal feature.
//
// Hooks into Server.Start after channel tools register but before
// injectIris (so Iris can pick up the send_to_remote_agent tool).
func (s *Server) startPeering(_ context.Context) {
	addr := os.Getenv("VEGA_PEERING_ADDR")
	if addr == "" {
		return
	}
	provider, ok := s.store.(peeringStoreProvider)
	if !ok {
		slog.Warn("peering: VEGA_PEERING_ADDR set but store does not support peering — ignoring", "kind", s.cfg.DBKind)
		return
	}
	pStore := provider.PeeringStore()

	dispatcher := newPeeringDispatcher(s.interp)
	cfg := peering.NodeConfig{Store: pStore, Dispatcher: dispatcher}
	if s.callerResolver != nil {
		// Background path: no peer-supplied user identity yet (AIRE v0.2
		// will fix that). Resolve to the configured default user so the
		// BYOK key for this tenant is attached before the agent runs.
		resolver := s.callerResolver
		cfg.WrapInboundContext = func(ctx context.Context) context.Context {
			return resolver(ctx, "")
		}
	}
	node, err := peering.NewNode(cfg)
	if err != nil {
		slog.Error("peering: NewNode failed; federation disabled", "error", err)
		return
	}
	// TODO: provision proper TLS certs for production. DevTLSConfig is
	// self-signed + ALPN-correct, fine for trusted-peers-with-pinned-secrets
	// while AIRE v0.2 (DIDs) isn't out yet.
	tlsConf := aire.DevTLSConfig()
	if err := node.Start(addr, tlsConf); err != nil {
		slog.Error("peering: listener Start failed; federation disabled", "error", err, "addr", addr)
		return
	}
	s.peeringStore = pStore
	s.peeringNode = node
	s.peeringDialer = peering.NewDialer(pStore, node.Signer(), tlsConf)

	// Register the federation toolset on the interpreter so injectIris
	// (called next) can include them in Iris's tool schema.
	s.registerPeeringTools()

	slog.Info("peering: started", "node_id", node.NodeID(), "addr", node.Addr())
}

// peeringEnabled reports whether the peering subsystem started successfully.
// Used by injectIris to decide whether to pass the peering tools as extras.
func (s *Server) peeringEnabled() bool {
	return s.peeringNode != nil
}

// stopPeering tears down the Dialer and Node. Safe to call when peering
// never started.
func (s *Server) stopPeering() {
	if s.peeringDialer != nil {
		s.peeringDialer.Close()
		s.peeringDialer = nil
	}
	if s.peeringNode != nil {
		_ = s.peeringNode.Stop()
		s.peeringNode = nil
	}
}

// peeringDispatcher adapts the Interpreter's StreamToAgent to the narrow
// peering.Dispatcher interface. Text deltas become response chunks; tool
// activity is dropped (the peer wants the final assistant text, not the
// local tool timeline); the final Done event's metrics populate
// DispatchStats so the audit log carries real numbers.
type peeringDispatcher struct {
	interp *dsl.Interpreter
}

func newPeeringDispatcher(interp *dsl.Interpreter) *peeringDispatcher {
	return &peeringDispatcher{interp: interp}
}

func (d *peeringDispatcher) Dispatch(
	ctx context.Context,
	agent, message string,
	emit func([]byte) error,
) (peering.DispatchStats, error) {
	stream, err := d.interp.StreamToAgent(ctx, agent, message)
	if err != nil {
		return peering.DispatchStats{}, err
	}
	var stats peering.DispatchStats
	for evt := range stream.Events() {
		switch evt.Type {
		case vega.ChatEventTextDelta:
			if evt.Delta == "" {
				continue
			}
			if err := emit([]byte(evt.Delta)); err != nil {
				return stats, err
			}
		case vega.ChatEventDone:
			if evt.Metrics != nil {
				stats.TokensIn = evt.Metrics.InputTokens
				stats.TokensOut = evt.Metrics.OutputTokens
				stats.CostUSD = evt.Metrics.CostUSD
			}
		case vega.ChatEventError:
			if evt.Error != "" {
				return stats, fmt.Errorf("agent error (%s): %s", evt.Code, evt.Error)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}
