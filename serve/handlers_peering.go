package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/everydev1618/govega/serve/peering"
)

// peeringEnabledGuard responds with 404 when peering isn't enabled — so the
// frontend's pill + modal naturally hide themselves when federation is off.
// Returning 404 (rather than 503) matches the "feature absent" semantics
// the React hooks already handle for other optional endpoints.
func (s *Server) peeringEnabledGuard(w http.ResponseWriter) bool {
	if !s.peeringEnabled() {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "peering not enabled (set VEGA_PEERING_ADDR)"})
		return false
	}
	return true
}

// PeeringStatusResponse is what the header pill polls. NodeID is shown
// read-only in the modal so the user can share it during peer setup.
type PeeringStatusResponse struct {
	Enabled    bool   `json:"enabled"`
	NodeID     string `json:"node_id"`
	ListenAddr string `json:"listen_addr"`
	PeerCount  int    `json:"peer_count"`
}

func (s *Server) handlePeeringStatus(w http.ResponseWriter, _ *http.Request) {
	resp := PeeringStatusResponse{Enabled: s.peeringEnabled()}
	if !resp.Enabled {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.NodeID = s.peeringNode.NodeID()
	resp.ListenAddr = s.peeringNode.Addr()
	peers, _ := s.peeringStore.ListPeers()
	resp.PeerCount = len(peers)
	writeJSON(w, http.StatusOK, resp)
}

// --- Peers ---

type peerDTO struct {
	NodeID     string `json:"node_id"`
	Handle     string `json:"handle"`
	Endpoint   string `json:"endpoint"`
	TrustLevel string `json:"trust_level"`
	AddedAt    string `json:"added_at,omitempty"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

func peerToDTO(p peering.Peer) peerDTO {
	d := peerDTO{
		NodeID:     p.NodeID,
		Handle:     p.Handle,
		Endpoint:   p.Endpoint,
		TrustLevel: string(p.TrustLevel),
		Notes:      p.Notes,
	}
	if !p.AddedAt.IsZero() {
		d.AddedAt = p.AddedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !p.LastSeenAt.IsZero() {
		d.LastSeenAt = p.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return d
}

func (s *Server) handleListPeers(w http.ResponseWriter, _ *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	peers, err := s.peeringStore.ListPeers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]peerDTO, 0, len(peers))
	for _, p := range peers {
		out = append(out, peerToDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

type addPeerRequest struct {
	NodeID       string `json:"node_id"`
	Handle       string `json:"handle"`
	Endpoint     string `json:"endpoint"`
	SharedSecret string `json:"shared_secret"`
	Notes        string `json:"notes"`
}

func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	var req addPeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON"})
		return
	}
	if req.NodeID == "" || req.Endpoint == "" || req.SharedSecret == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "node_id, endpoint, and shared_secret are required"})
		return
	}
	p := peering.Peer{
		NodeID:       req.NodeID,
		Handle:       req.Handle,
		Endpoint:     req.Endpoint,
		SharedSecret: req.SharedSecret,
		TrustLevel:   peering.TrustScoped,
		Notes:        req.Notes,
	}
	if err := s.peeringStore.UpsertPeer(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, peerToDTO(p))
}

type updatePeerRequest struct {
	TrustLevel string `json:"trust_level"`
}

func (s *Server) handleUpdatePeer(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	nodeID := r.PathValue("nodeID")
	existing, err := s.peeringStore.GetPeer(nodeID)
	if err != nil || existing == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "peer not found"})
		return
	}
	var req updatePeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON"})
		return
	}
	if req.TrustLevel != "" {
		switch peering.TrustLevel(req.TrustLevel) {
		case peering.TrustTrusted, peering.TrustScoped, peering.TrustPaused:
			existing.TrustLevel = peering.TrustLevel(req.TrustLevel)
		default:
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "trust_level must be trusted, scoped, or paused"})
			return
		}
	}
	if err := s.peeringStore.UpsertPeer(*existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, peerToDTO(*existing))
}

func (s *Server) handleDeletePeer(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	nodeID := r.PathValue("nodeID")
	// Cascade: remove all grants for this peer first.
	grants, _ := s.peeringStore.ListGrants(nodeID)
	for _, g := range grants {
		_ = s.peeringStore.DeleteGrant(g.PeerNodeID, g.LocalAgent)
	}
	if err := s.peeringStore.DeletePeer(nodeID); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": nodeID, "grants_removed": len(grants)})
}

// --- Grants ---

type grantDTO struct {
	PeerNodeID     string `json:"peer_node_id"`
	LocalAgent     string `json:"local_agent"`
	MaxTokensPerOp int    `json:"max_tokens_per_op"`
	MaxOpsPerHour  int    `json:"max_ops_per_hour"`
	Active         bool   `json:"active"`
	CreatedAt      string `json:"created_at,omitempty"`
}

func grantToDTO(g peering.Grant) grantDTO {
	d := grantDTO{
		PeerNodeID:     g.PeerNodeID,
		LocalAgent:     g.LocalAgent,
		MaxTokensPerOp: g.MaxTokensPerOp,
		MaxOpsPerHour:  g.MaxOpsPerHour,
		Active:         g.Active,
	}
	if !g.CreatedAt.IsZero() {
		d.CreatedAt = g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return d
}

func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	peerFilter := r.URL.Query().Get("peer")
	grants, err := s.peeringStore.ListGrants(peerFilter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]grantDTO, 0, len(grants))
	for _, g := range grants {
		out = append(out, grantToDTO(g))
	}
	writeJSON(w, http.StatusOK, out)
}

type upsertGrantRequest struct {
	MaxTokensPerOp int  `json:"max_tokens_per_op"`
	MaxOpsPerHour  int  `json:"max_ops_per_hour"`
	Active         bool `json:"active"`
}

func (s *Server) handleUpsertGrant(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	peerID := r.PathValue("peerID")
	agent := r.PathValue("agent")
	if peerID == "" || agent == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "peer and agent are required"})
		return
	}
	var req upsertGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON"})
		return
	}
	g := peering.Grant{
		PeerNodeID:     peerID,
		LocalAgent:     peering.CanonicalizeAgent(agent),
		MaxTokensPerOp: req.MaxTokensPerOp,
		MaxOpsPerHour:  req.MaxOpsPerHour,
		Active:         req.Active,
	}
	if err := s.peeringStore.UpsertGrant(g); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, grantToDTO(g))
}

func (s *Server) handleDeleteGrant(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	peerID := r.PathValue("peerID")
	agent := peering.CanonicalizeAgent(r.PathValue("agent"))
	if err := s.peeringStore.DeleteGrant(peerID, agent); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted_peer": peerID, "deleted_agent": agent})
}

// --- Audit + live ops ---

type auditDTO struct {
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	Direction    string  `json:"direction"`
	PeerNodeID   string  `json:"peer_node_id"`
	PeerHandle   string  `json:"peer_handle,omitempty"`
	Agent        string  `json:"agent"`
	OpID         int64   `json:"op_id"`
	TokensIn     int     `json:"tokens_in"`
	TokensOut    int     `json:"tokens_out"`
	CostUSD      float64 `json:"cost_usd"`
	Status       string  `json:"status"`
	DenialReason string  `json:"denial_reason,omitempty"`
	DurationMS   int     `json:"duration_ms"`
}

func auditToDTO(e peering.AuditEntry) auditDTO {
	return auditDTO{
		ID:           e.ID,
		Timestamp:    e.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		Direction:    e.Direction,
		PeerNodeID:   e.PeerNodeID,
		PeerHandle:   e.PeerHandle,
		Agent:        e.Agent,
		OpID:         e.OpID,
		TokensIn:     e.TokensIn,
		TokensOut:    e.TokensOut,
		CostUSD:      e.CostUSD,
		Status:       e.Status,
		DenialReason: e.DenialReason,
		DurationMS:   e.DurationMS,
	}
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	q := r.URL.Query()
	filter := peering.AuditFilter{
		PeerNodeID: q.Get("peer"),
		Agent:      q.Get("agent"),
		Direction:  q.Get("direction"),
	}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	rows, err := s.peeringStore.ListAudit(filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]auditDTO, 0, len(rows))
	for _, e := range rows {
		out = append(out, auditToDTO(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// InviteDTO is the shape both sides exchange to set up a peer relationship.
// All three fields are required for the recipient to dial back and prove
// possession of the shared secret. Format is intentionally JSON so users
// can copy-paste over any secure channel (Signal, encrypted email, etc.).
type InviteDTO struct {
	NodeID       string `json:"node_id"`
	Endpoint     string `json:"endpoint"`
	SharedSecret string `json:"shared_secret"`
}

// handleCreateInvite mints a fresh invite for the local node. Stateless —
// nothing is persisted until the receiving side accepts and the local side
// gets a return invite + persists via /peers.
//
// Endpoint defaults to VEGA_PEERING_PUBLIC_ENDPOINT when set, falling back
// to the listener's advertised address. Operators behind NAT should set
// the public env so the invite carries something the peer can actually dial.
func (s *Server) handleCreateInvite(w http.ResponseWriter, _ *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	secret, err := peering.NewSharedSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, InviteDTO{
		NodeID:       s.peeringNode.NodeID(),
		Endpoint:     s.peeringEndpointForInvite(),
		SharedSecret: secret,
	})
}

// handleReturnInvite produces an invite that reuses the shared secret from
// a received invite, so both sides end up with the same secret in their
// peer tables. Body is the incoming InviteDTO; response is the reciprocal
// for the recipient to send back to the original inviter.
func (s *Server) handleReturnInvite(w http.ResponseWriter, r *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	var in InviteDTO
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid invite JSON"})
		return
	}
	if in.SharedSecret == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invite is missing shared_secret"})
		return
	}
	writeJSON(w, http.StatusOK, InviteDTO{
		NodeID:       s.peeringNode.NodeID(),
		Endpoint:     s.peeringEndpointForInvite(),
		SharedSecret: in.SharedSecret,
	})
}

// peeringEndpointForInvite returns the address peers should dial. Prefers
// VEGA_PEERING_PUBLIC_ENDPOINT (set by operators behind NAT or running on
// non-routable interfaces) over the listener address.
func (s *Server) peeringEndpointForInvite() string {
	if pub := os.Getenv("VEGA_PEERING_PUBLIC_ENDPOINT"); pub != "" {
		return pub
	}
	return s.peeringNode.Addr()
}

// handleLiveOps returns audit rows where Status == 'started'. v1 polls this
// endpoint; v2 will upgrade to SSE so the modal's Live Ops tab is real-time
// without polling cost.
func (s *Server) handleLiveOps(w http.ResponseWriter, _ *http.Request) {
	if !s.peeringEnabledGuard(w) {
		return
	}
	rows, err := s.peeringStore.ListAudit(peering.AuditFilter{Limit: 200})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]auditDTO, 0)
	for _, e := range rows {
		if e.Status == peering.AuditStatusStarted {
			out = append(out, auditToDTO(e))
		}
	}
	writeJSON(w, http.StatusOK, out)
}
