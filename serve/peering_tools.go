package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/everydev1618/govega/serve/peering"
	"github.com/everydev1618/govega/tools"
)

// Tool names registered on the interpreter when peering is enabled.
// Exported as a slice so the Iris injection site can hand the list to
// InjectIris without re-spelling each name.
var peeringToolNames = []string{
	"send_to_remote_agent",
	"list_peers",
	"add_peer",
	"remove_peer",
	"grant_peer_access",
	"revoke_peer_access",
	"local_node_id",
}

// registerPeeringTools registers the federation toolset on the interpreter
// once peering is up. Called from startPeering.
func (s *Server) registerPeeringTools() {
	t := s.interp.Tools()
	t.Register("send_to_remote_agent", s.newSendToRemoteAgentTool())
	t.Register("list_peers", s.newListPeersTool())
	t.Register("add_peer", s.newAddPeerTool())
	t.Register("remove_peer", s.newRemovePeerTool())
	t.Register("grant_peer_access", s.newGrantPeerAccessTool())
	t.Register("revoke_peer_access", s.newRevokePeerAccessTool())
	t.Register("local_node_id", s.newLocalNodeIDTool())
}

func (s *Server) newSendToRemoteAgentTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Invoke a remote agent on a trusted peer orchestrator over AIRE. " +
			"Resolves `peer` by handle (e.g. '@alice@nous') or NodeID. Streams the " +
			"agent's response back into the current conversation. Blocks until the " +
			"remote agent completes — use sparingly for interactive turns.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			peerRef, _ := params["peer"].(string)
			agent, _ := params["agent"].(string)
			message, _ := params["message"].(string)
			if peerRef == "" || agent == "" || message == "" {
				return "", fmt.Errorf("peer, agent, and message are required")
			}
			p, err := s.resolvePeer(peerRef)
			if err != nil {
				return "", err
			}
			var collected strings.Builder
			remErr, callErr := s.peeringDialer.SendToRemoteAgent(ctx, *p, agent, message, func(b []byte) error {
				collected.Write(b)
				return nil
			})
			if callErr != nil {
				return "", fmt.Errorf("call %s/%s: %w", peerRef, agent, callErr)
			}
			if remErr != nil {
				return "", fmt.Errorf("peer rejected: %s", remErr.Error())
			}
			return collected.String(), nil
		}),
		Params: map[string]tools.ParamDef{
			"peer":    {Type: "string", Description: "Peer handle ('@alice@nous') or NodeID ('vega:...')", Required: true},
			"agent":   {Type: "string", Description: "Local agent name on the peer (e.g. 'researcher')", Required: true},
			"message": {Type: "string", Description: "Task or question for the remote agent", Required: true},
		},
	}
}

func (s *Server) newListPeersTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "List the orchestrator peers configured for federation. Returns JSON " +
			"with handle, node_id, endpoint, trust_level, last_seen_at.",
		Fn: tools.ToolFunc(func(_ context.Context, _ map[string]any) (string, error) {
			peers, err := s.peeringStore.ListPeers()
			if err != nil {
				return "", err
			}
			type item struct {
				Handle     string `json:"handle"`
				NodeID     string `json:"node_id"`
				Endpoint   string `json:"endpoint"`
				TrustLevel string `json:"trust_level"`
				LastSeen   string `json:"last_seen_at,omitempty"`
			}
			out := make([]item, 0, len(peers))
			for _, p := range peers {
				it := item{Handle: p.Handle, NodeID: p.NodeID, Endpoint: p.Endpoint, TrustLevel: string(p.TrustLevel)}
				if !p.LastSeenAt.IsZero() {
					it.LastSeen = p.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z")
				}
				out = append(out, it)
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			return string(b), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

func (s *Server) newAddPeerTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Add a trusted peer orchestrator. Requires handle, node_id, endpoint, " +
			"and a shared_secret you've agreed on out-of-band. The peer must add you " +
			"symmetrically using your local NodeID (call local_node_id) before invokes work.",
		Fn: tools.ToolFunc(func(_ context.Context, params map[string]any) (string, error) {
			nodeID, _ := params["node_id"].(string)
			handle, _ := params["handle"].(string)
			endpoint, _ := params["endpoint"].(string)
			secret, _ := params["shared_secret"].(string)
			if nodeID == "" || endpoint == "" || secret == "" {
				return "", fmt.Errorf("node_id, endpoint, and shared_secret are required")
			}
			p := peering.Peer{
				NodeID:       nodeID,
				Handle:       handle,
				Endpoint:     endpoint,
				SharedSecret: secret,
				TrustLevel:   peering.TrustScoped,
			}
			if err := s.peeringStore.UpsertPeer(p); err != nil {
				return "", err
			}
			return fmt.Sprintf("Added peer %s (handle=%s). Grant agents via grant_peer_access before invokes are accepted.", nodeID, handle), nil
		}),
		Params: map[string]tools.ParamDef{
			"node_id":       {Type: "string", Description: "Peer NodeID (e.g. 'vega:01ABC...')", Required: true},
			"handle":        {Type: "string", Description: "Human-friendly handle ('@alice@nous'); optional"},
			"endpoint":      {Type: "string", Description: "QUIC endpoint, e.g. 'alice.example.com:4433'", Required: true},
			"shared_secret": {Type: "string", Description: "Pre-shared secret (32+ bytes recommended)", Required: true},
		},
	}
}

func (s *Server) newRemovePeerTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Remove a peer orchestrator. Also drops all grants for that peer (cascade).",
		Fn: tools.ToolFunc(func(_ context.Context, params map[string]any) (string, error) {
			peerRef, _ := params["peer"].(string)
			if peerRef == "" {
				return "", fmt.Errorf("peer is required")
			}
			p, err := s.resolvePeer(peerRef)
			if err != nil {
				return "", err
			}
			grants, _ := s.peeringStore.ListGrants(p.NodeID)
			for _, g := range grants {
				_ = s.peeringStore.DeleteGrant(g.PeerNodeID, g.LocalAgent)
			}
			if err := s.peeringStore.DeletePeer(p.NodeID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed peer %s and %d grant(s).", p.NodeID, len(grants)), nil
		}),
		Params: map[string]tools.ParamDef{
			"peer": {Type: "string", Description: "Peer handle or NodeID", Required: true},
		},
	}
}

func (s *Server) newGrantPeerAccessTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Allow a peer to invoke a specific local agent. Default limits: " +
			"8000 tokens per op, 30 ops per hour. Override via max_tokens_per_op / max_ops_per_hour.",
		Fn: tools.ToolFunc(func(_ context.Context, params map[string]any) (string, error) {
			peerRef, _ := params["peer"].(string)
			agent, _ := params["agent"].(string)
			if peerRef == "" || agent == "" {
				return "", fmt.Errorf("peer and agent are required")
			}
			p, err := s.resolvePeer(peerRef)
			if err != nil {
				return "", err
			}
			g := peering.Grant{
				PeerNodeID:     p.NodeID,
				LocalAgent:     peering.CanonicalizeAgent(agent),
				MaxTokensPerOp: intParam(params, "max_tokens_per_op", 8000),
				MaxOpsPerHour:  intParam(params, "max_ops_per_hour", 30),
				Active:         true,
			}
			if err := s.peeringStore.UpsertGrant(g); err != nil {
				return "", err
			}
			return fmt.Sprintf("Granted %s → %s (max %d tokens/op, %d ops/hour).", p.NodeID, g.LocalAgent, g.MaxTokensPerOp, g.MaxOpsPerHour), nil
		}),
		Params: map[string]tools.ParamDef{
			"peer":              {Type: "string", Description: "Peer handle or NodeID", Required: true},
			"agent":             {Type: "string", Description: "Local agent name to expose", Required: true},
			"max_tokens_per_op": {Type: "number", Description: "Default 8000"},
			"max_ops_per_hour":  {Type: "number", Description: "Default 30"},
		},
	}
}

func (s *Server) newRevokePeerAccessTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Remove a (peer, agent) grant so the peer can no longer invoke that agent.",
		Fn: tools.ToolFunc(func(_ context.Context, params map[string]any) (string, error) {
			peerRef, _ := params["peer"].(string)
			agent, _ := params["agent"].(string)
			if peerRef == "" || agent == "" {
				return "", fmt.Errorf("peer and agent are required")
			}
			p, err := s.resolvePeer(peerRef)
			if err != nil {
				return "", err
			}
			if err := s.peeringStore.DeleteGrant(p.NodeID, peering.CanonicalizeAgent(agent)); err != nil {
				return "", err
			}
			return fmt.Sprintf("Revoked %s → %s.", p.NodeID, agent), nil
		}),
		Params: map[string]tools.ParamDef{
			"peer":  {Type: "string", Description: "Peer handle or NodeID", Required: true},
			"agent": {Type: "string", Description: "Local agent name to revoke", Required: true},
		},
	}
}

func (s *Server) newLocalNodeIDTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Return this orchestrator's NodeID (share with peers when establishing trust).",
		Fn: tools.ToolFunc(func(_ context.Context, _ map[string]any) (string, error) {
			if s.peeringNode == nil {
				return "", fmt.Errorf("peering is disabled")
			}
			return s.peeringNode.NodeID(), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// resolvePeer accepts either a handle ('@alice@nous') or a NodeID
// ('vega:...') and returns the matching Peer row, or an error if not found.
func (s *Server) resolvePeer(ref string) (*peering.Peer, error) {
	// NodeID path is exact match.
	if p, _ := s.peeringStore.GetPeer(ref); p != nil {
		return p, nil
	}
	// Handle path scans (the table is small enough that an index isn't
	// urgent; revisit when peer count grows).
	peers, err := s.peeringStore.ListPeers()
	if err != nil {
		return nil, err
	}
	for i := range peers {
		if peers[i].Handle == ref {
			return &peers[i], nil
		}
	}
	return nil, fmt.Errorf("peer not found: %q (try `list_peers` to see configured peers)", ref)
}

// intParam pulls a number from params with a default fallback. Tool args
// arrive as `any`; JSON numbers decode as float64, but we accept int too.
func intParam(params map[string]any, key string, def int) int {
	if v, ok := params[key]; ok && v != nil {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return def
}
