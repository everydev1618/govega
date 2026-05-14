package peering

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStore returns a fresh Store backed by an in-memory SQLite database.
// Each test gets its own DB so rows from one test never leak into another.
func newTestStore(t *testing.T) Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := ApplySQLiteSchema(db); err != nil {
		t.Fatalf("ApplySQLiteSchema: %v", err)
	}
	return NewSQLiteStorage(db)
}

// newTestStorePG returns nil unless VEGA_TEST_POSTGRES_URL is set, matching
// the dual-test convention in serve/store_dual_test.go.
func newTestStorePG(t *testing.T) Store {
	t.Helper()
	url := os.Getenv("VEGA_TEST_POSTGRES_URL")
	if url == "" {
		return nil
	}
	// Postgres-backed peering store tests are gated; we don't write the
	// schema-isolation glue here because the outer serve package already
	// owns the migration runner. When postgres becomes a default CI target
	// we'll wire the same private-schema pattern as serve/store_dual_test.go.
	t.Skip("peering postgres tests require running migrations; covered by serve-level dual tests")
	return nil
}

func forEachStore(t *testing.T, fn func(t *testing.T, store Store)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, newTestStore(t)) })
	t.Run("postgres", func(t *testing.T) {
		store := newTestStorePG(t)
		if store == nil {
			t.Skip("VEGA_TEST_POSTGRES_URL not set")
		}
		fn(t, store)
	})
}

func samplePeer() Peer {
	return Peer{
		NodeID:       "vega:peer-1",
		Handle:       "@alice@nous",
		Endpoint:     "quic://alice.example.com:4433",
		SharedSecret: "deadbeef-32-byte-hex-here-not-real",
		TrustLevel:   TrustScoped,
		AddedBy:      "user-1",
		Notes:        "test peer",
	}
}

func sampleGrant(peerNodeID string) Grant {
	return Grant{
		PeerNodeID:      peerNodeID,
		LocalAgent:      "researcher",
		MaxTokensPerOp:  8000,
		MaxOpsPerHour:   30,
		Active:          true,
	}
}

// --- Peer CRUD ---

func TestPeer_RoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p := samplePeer()
		if err := s.UpsertPeer(p); err != nil {
			t.Fatalf("UpsertPeer: %v", err)
		}
		got, err := s.GetPeer(p.NodeID)
		if err != nil {
			t.Fatalf("GetPeer: %v", err)
		}
		if got == nil {
			t.Fatal("GetPeer returned nil")
		}
		if got.NodeID != p.NodeID || got.Handle != p.Handle || got.Endpoint != p.Endpoint ||
			got.SharedSecret != p.SharedSecret || got.TrustLevel != p.TrustLevel ||
			got.AddedBy != p.AddedBy || got.Notes != p.Notes {
			t.Fatalf("round-trip mismatch.\nwant=%+v\ngot=%+v", p, got)
		}
		if got.AddedAt.IsZero() {
			t.Fatal("AddedAt should be populated on insert")
		}
	})
}

func TestPeer_UpsertOverwrites(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p := samplePeer()
		if err := s.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
		p.Handle = "@alice-renamed@nous"
		p.TrustLevel = TrustPaused
		if err := s.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetPeer(p.NodeID)
		if got.Handle != "@alice-renamed@nous" || got.TrustLevel != TrustPaused {
			t.Fatalf("upsert did not overwrite, got=%+v", got)
		}
	})
}

func TestPeer_GetMissingReturnsNil(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		got, err := s.GetPeer("vega:nope")
		if err != nil {
			t.Fatalf("expected no error for missing peer, got %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil peer for missing nodeID, got %+v", got)
		}
	})
}

func TestPeer_List(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p1 := samplePeer()
		p2 := samplePeer()
		p2.NodeID = "vega:peer-2"
		p2.Handle = "@bob@nous"
		if err := s.UpsertPeer(p1); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertPeer(p2); err != nil {
			t.Fatal(err)
		}
		peers, err := s.ListPeers()
		if err != nil {
			t.Fatal(err)
		}
		if len(peers) != 2 {
			t.Fatalf("expected 2 peers, got %d", len(peers))
		}
	})
}

func TestPeer_Delete(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p := samplePeer()
		_ = s.UpsertPeer(p)
		if err := s.DeletePeer(p.NodeID); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetPeer(p.NodeID)
		if got != nil {
			t.Fatal("DeletePeer did not remove row")
		}
	})
}

// --- Grant CRUD ---

func TestGrant_RoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p := samplePeer()
		_ = s.UpsertPeer(p)
		g := sampleGrant(p.NodeID)
		if err := s.UpsertGrant(g); err != nil {
			t.Fatalf("UpsertGrant: %v", err)
		}
		got, err := s.GetGrant(g.PeerNodeID, g.LocalAgent)
		if err != nil {
			t.Fatalf("GetGrant: %v", err)
		}
		if got == nil {
			t.Fatal("GetGrant returned nil for known grant")
		}
		if got.PeerNodeID != g.PeerNodeID || got.LocalAgent != g.LocalAgent ||
			got.MaxTokensPerOp != g.MaxTokensPerOp || got.MaxOpsPerHour != g.MaxOpsPerHour ||
			got.Active != g.Active {
			t.Fatalf("grant round-trip mismatch.\nwant=%+v\ngot=%+v", g, got)
		}
	})
}

func TestGrant_DefaultDeny(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		got, err := s.GetGrant("vega:peer-1", "researcher")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatal("expected nil grant for un-granted (peer, agent)")
		}
	})
}

func TestGrant_ListForPeer(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p := samplePeer()
		_ = s.UpsertPeer(p)
		g1 := sampleGrant(p.NodeID)
		g2 := sampleGrant(p.NodeID)
		g2.LocalAgent = "writer"
		_ = s.UpsertGrant(g1)
		_ = s.UpsertGrant(g2)

		grants, err := s.ListGrants(p.NodeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(grants) != 2 {
			t.Fatalf("expected 2 grants, got %d", len(grants))
		}
	})
}

func TestGrant_Revoke(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		p := samplePeer()
		_ = s.UpsertPeer(p)
		g := sampleGrant(p.NodeID)
		_ = s.UpsertGrant(g)
		if err := s.DeleteGrant(g.PeerNodeID, g.LocalAgent); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetGrant(g.PeerNodeID, g.LocalAgent)
		if got != nil {
			t.Fatal("grant should be gone after DeleteGrant")
		}
	})
}

// --- Audit log ---

func sampleAudit() AuditEntry {
	return AuditEntry{
		Direction:    DirectionInbound,
		PeerNodeID:   "vega:peer-1",
		PeerHandle:   "@alice@nous",
		Agent:        "researcher",
		OpID:         12345,
		Status:       AuditStatusStarted,
		TokensIn:     0,
		TokensOut:    0,
		CostUSD:      0,
	}
}

func TestAudit_InsertAndList(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		e := sampleAudit()
		id, err := s.InsertAudit(e)
		if err != nil {
			t.Fatalf("InsertAudit: %v", err)
		}
		if id == 0 {
			t.Fatal("expected non-zero id from InsertAudit")
		}

		got, err := s.ListAudit(AuditFilter{Limit: 10})
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 audit row, got %d", len(got))
		}
		if got[0].PeerNodeID != e.PeerNodeID || got[0].Agent != e.Agent || got[0].OpID != e.OpID {
			t.Fatalf("audit round-trip mismatch.\nwant=%+v\ngot=%+v", e, got[0])
		}
		if got[0].Timestamp.IsZero() {
			t.Fatal("audit Timestamp should be populated on insert")
		}
	})
}

func TestAudit_UpdateFinalizesEntry(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		e := sampleAudit()
		id, _ := s.InsertAudit(e)

		err := s.FinalizeAudit(id, AuditFinalize{
			Status:       AuditStatusOK,
			TokensIn:     100,
			TokensOut:    250,
			CostUSD:      0.0042,
			DurationMS:   1234,
			DenialReason: "",
		})
		if err != nil {
			t.Fatalf("FinalizeAudit: %v", err)
		}

		rows, _ := s.ListAudit(AuditFilter{Limit: 10})
		if rows[0].Status != AuditStatusOK || rows[0].TokensOut != 250 || rows[0].CostUSD < 0.004 {
			t.Fatalf("finalize did not update row: %+v", rows[0])
		}
		if rows[0].DurationMS != 1234 {
			t.Fatalf("DurationMS = %d, want 1234", rows[0].DurationMS)
		}
	})
}

func TestAudit_FilterByPeerAndDirection(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		// Two peers, one inbound + one outbound each.
		mk := func(peer, dir string) AuditEntry {
			e := sampleAudit()
			e.PeerNodeID = peer
			e.Direction = dir
			return e
		}
		_, _ = s.InsertAudit(mk("vega:peer-1", DirectionInbound))
		_, _ = s.InsertAudit(mk("vega:peer-1", DirectionOutbound))
		_, _ = s.InsertAudit(mk("vega:peer-2", DirectionInbound))

		got, err := s.ListAudit(AuditFilter{PeerNodeID: "vega:peer-1", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 rows for peer-1, got %d", len(got))
		}

		gotIn, _ := s.ListAudit(AuditFilter{Direction: DirectionInbound, Limit: 10})
		if len(gotIn) != 2 {
			t.Fatalf("expected 2 inbound rows, got %d", len(gotIn))
		}
	})
}

func TestAudit_CountOpsInWindow(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		// Three ops for (peer-1, researcher); the count should reflect
		// only the ones whose timestamp falls within the window.
		insertAt := func(ts time.Time, status string) {
			e := sampleAudit()
			e.Timestamp = ts
			e.Status = status
			if _, err := s.InsertAudit(e); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now().UTC()
		insertAt(now.Add(-10*time.Minute), AuditStatusOK)
		insertAt(now.Add(-30*time.Minute), AuditStatusOK)
		insertAt(now.Add(-90*time.Minute), AuditStatusOK)

		// Window: last hour.
		n, err := s.CountOpsInWindow("vega:peer-1", "researcher", now.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("expected 2 ops in last hour, got %d", n)
		}
	})
}

func TestAudit_CountOpsInWindow_ExcludesDenied(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		// Denials still write rows (for audit) but MUST NOT count against
		// the rate limit — otherwise a flood of denied ops would lock out
		// future legitimate ones.
		now := time.Now().UTC()
		mk := func(status string) AuditEntry {
			e := sampleAudit()
			e.Timestamp = now.Add(-10 * time.Minute)
			e.Status = status
			return e
		}
		_, _ = s.InsertAudit(mk(AuditStatusOK))
		_, _ = s.InsertAudit(mk(AuditStatusDenied))

		n, err := s.CountOpsInWindow("vega:peer-1", "researcher", now.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("denied rows should not count toward rate limit; got %d", n)
		}
	})
}

// --- Schema sanity ---

func TestApplySQLiteSchema_Idempotent(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	if err := ApplySQLiteSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := ApplySQLiteSchema(db); err != nil {
		t.Fatalf("second apply should be idempotent, got %v", err)
	}
}

func TestApplySQLiteSchema_FailsOnNilDB(t *testing.T) {
	err := ApplySQLiteSchema(nil)
	if !errors.Is(err, ErrNilDB) {
		t.Fatalf("expected ErrNilDB, got %v", err)
	}
}
