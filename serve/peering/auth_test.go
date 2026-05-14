package peering

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// Shared fixtures.
var (
	testSecret = []byte("the-quick-brown-fox-jumps-over-the-lazy-dog-32!")
	testNodeID = "vega:01234567-89ab-cdef-0123-456789abcdef"
	testNonce  = []byte("0123456789abcdef") // 16 bytes
	testTime   = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
)

func makeClaim() AuthClaim {
	return AuthClaim{
		NodeID:    testNodeID,
		Nonce:     append([]byte(nil), testNonce...),
		Timestamp: testTime,
	}
}

func TestComputeAuth_StableForSameInputs(t *testing.T) {
	a := ComputeAuth(testSecret, makeClaim())
	b := ComputeAuth(testSecret, makeClaim())
	if !bytes.Equal(a, b) {
		t.Fatalf("ComputeAuth should be deterministic; got %x vs %x", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("expected 32-byte HMAC-SHA256 output, got %d", len(a))
	}
}

func TestComputeAuth_DifferentSecretsDiffer(t *testing.T) {
	a := ComputeAuth(testSecret, makeClaim())
	b := ComputeAuth([]byte("different-secret-of-equal-or-different-length!!"), makeClaim())
	if bytes.Equal(a, b) {
		t.Fatal("MAC must change when secret changes")
	}
}

func TestComputeAuth_FieldsAreBound(t *testing.T) {
	base := ComputeAuth(testSecret, makeClaim())

	withDifferentNode := makeClaim()
	withDifferentNode.NodeID = "vega:other"
	if bytes.Equal(base, ComputeAuth(testSecret, withDifferentNode)) {
		t.Fatal("MAC must change when NodeID changes")
	}

	withDifferentNonce := makeClaim()
	withDifferentNonce.Nonce = []byte("ffffffffffffffff")
	if bytes.Equal(base, ComputeAuth(testSecret, withDifferentNonce)) {
		t.Fatal("MAC must change when nonce changes")
	}

	withDifferentTime := makeClaim()
	withDifferentTime.Timestamp = testTime.Add(time.Second)
	if bytes.Equal(base, ComputeAuth(testSecret, withDifferentTime)) {
		t.Fatal("MAC must change when timestamp changes")
	}
}

func TestComputeAuth_AmbiguityResistance(t *testing.T) {
	// A naive concat encoding ("AB" + "CD" == "A" + "BCD") would make these
	// two claims produce the same MAC. Length-prefix encoding prevents that.
	c1 := AuthClaim{NodeID: "ab", Nonce: []byte("cd"), Timestamp: testTime}
	c2 := AuthClaim{NodeID: "a", Nonce: []byte("bcd"), Timestamp: testTime}
	if bytes.Equal(ComputeAuth(testSecret, c1), ComputeAuth(testSecret, c2)) {
		t.Fatal("encoding must be unambiguous across field boundaries")
	}
}

func TestVerifyAuth_HappyPath(t *testing.T) {
	claim := makeClaim()
	mac := ComputeAuth(testSecret, claim)
	if err := VerifyAuth(testSecret, testNodeID, claim, mac, testTime); err != nil {
		t.Fatalf("expected verify ok, got %v", err)
	}
}

func TestVerifyAuth_BadMAC(t *testing.T) {
	claim := makeClaim()
	mac := ComputeAuth(testSecret, claim)
	mac[0] ^= 0xff
	err := VerifyAuth(testSecret, testNodeID, claim, mac, testTime)
	if !errors.Is(err, ErrAuthBadMAC) {
		t.Fatalf("expected ErrAuthBadMAC, got %v", err)
	}
}

func TestVerifyAuth_WrongSecret(t *testing.T) {
	claim := makeClaim()
	mac := ComputeAuth(testSecret, claim)
	err := VerifyAuth([]byte("nope-nope-nope-nope-nope-nope-nope-nope-nope!"), testNodeID, claim, mac, testTime)
	if !errors.Is(err, ErrAuthBadMAC) {
		t.Fatalf("expected ErrAuthBadMAC for wrong secret, got %v", err)
	}
}

func TestVerifyAuth_NodeMismatch(t *testing.T) {
	claim := makeClaim()
	mac := ComputeAuth(testSecret, claim)
	err := VerifyAuth(testSecret, "vega:somebody-else", claim, mac, testTime)
	if !errors.Is(err, ErrAuthNodeMismatch) {
		t.Fatalf("expected ErrAuthNodeMismatch, got %v", err)
	}
}

func TestVerifyAuth_StaleClaim(t *testing.T) {
	claim := makeClaim()
	mac := ComputeAuth(testSecret, claim)
	now := testTime.Add(FreshnessWindow + time.Second)
	err := VerifyAuth(testSecret, testNodeID, claim, mac, now)
	if !errors.Is(err, ErrAuthStale) {
		t.Fatalf("expected ErrAuthStale, got %v", err)
	}
}

func TestVerifyAuth_AtFreshnessBoundary(t *testing.T) {
	claim := makeClaim()
	mac := ComputeAuth(testSecret, claim)
	// Exactly at the window boundary is still fresh; one nanosecond past is stale.
	atBoundary := testTime.Add(FreshnessWindow)
	if err := VerifyAuth(testSecret, testNodeID, claim, mac, atBoundary); err != nil {
		t.Fatalf("at-boundary claim should verify, got %v", err)
	}
	justPast := testTime.Add(FreshnessWindow + time.Nanosecond)
	if err := VerifyAuth(testSecret, testNodeID, claim, mac, justPast); !errors.Is(err, ErrAuthStale) {
		t.Fatalf("just-past-boundary claim should be stale, got %v", err)
	}
}

func TestVerifyAuth_FutureClaimRejected(t *testing.T) {
	claim := makeClaim()
	claim.Timestamp = testTime.Add(FutureSkew + time.Second)
	mac := ComputeAuth(testSecret, claim)
	err := VerifyAuth(testSecret, testNodeID, claim, mac, testTime)
	if !errors.Is(err, ErrAuthFuture) {
		t.Fatalf("expected ErrAuthFuture, got %v", err)
	}
}

func TestVerifyAuth_FutureWithinSkewOK(t *testing.T) {
	claim := makeClaim()
	claim.Timestamp = testTime.Add(FutureSkew / 2)
	mac := ComputeAuth(testSecret, claim)
	if err := VerifyAuth(testSecret, testNodeID, claim, mac, testTime); err != nil {
		t.Fatalf("expected small clock skew to be tolerated, got %v", err)
	}
}

func TestVerifyAuth_ShortNonce(t *testing.T) {
	claim := makeClaim()
	claim.Nonce = []byte("too-short") // 9 bytes; less than NonceLen
	mac := ComputeAuth(testSecret, claim)
	err := VerifyAuth(testSecret, testNodeID, claim, mac, testTime)
	if !errors.Is(err, ErrAuthShortNonce) {
		t.Fatalf("expected ErrAuthShortNonce, got %v", err)
	}
}

func TestNewNonce(t *testing.T) {
	a, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	if len(a) != NonceLen {
		t.Fatalf("nonce length = %d, want %d", len(a), NonceLen)
	}
	b, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce (second): %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two successive nonces collided — RNG broken")
	}
}
