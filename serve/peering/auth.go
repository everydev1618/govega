// Package peering provides Vega's orchestrator-to-orchestrator federation layer
// over the AIRE protocol. It is the only package in govega permitted to import
// github.com/aire-protocol/aire-go; everything else sees Vega-native types.
//
// See docs/peering-design.md for the design memo.
package peering

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"
)

// NonceLen is the required length in bytes for an auth nonce. Sized for
// HMAC-SHA256: 128 bits of entropy is plenty given the 5-minute freshness
// window in FreshnessWindow.
const NonceLen = 16

// FreshnessWindow bounds how old a claim's timestamp may be before VerifyAuth
// rejects it as a replay. Five minutes is generous for clock drift and slow
// links yet tight enough to make capture-and-replay infeasible in practice.
const FreshnessWindow = 5 * time.Minute

// FutureSkew is the small tolerance applied to claims whose timestamp is
// ahead of the verifier's clock — peers' clocks are not perfectly in sync.
const FutureSkew = 30 * time.Second

// AuthClaim is the tuple a peer signs to prove control of the shared secret
// for its declared NodeID. The verifier checks: (a) HMAC matches under the
// secret it has stored for ExpectedNodeID, (b) Timestamp is recent.
type AuthClaim struct {
	NodeID    string
	Nonce     []byte
	Timestamp time.Time
}

// Errors returned by VerifyAuth. Wrapped via fmt.Errorf("%w: ...") at call sites.
var (
	ErrAuthBadMAC       = errors.New("peering: HMAC mismatch")
	ErrAuthStale        = errors.New("peering: auth claim too old")
	ErrAuthFuture       = errors.New("peering: auth claim timestamp too far in future")
	ErrAuthNodeMismatch = errors.New("peering: NodeID mismatch")
	ErrAuthShortNonce   = errors.New("peering: nonce too short")
)

// encodeClaim produces the canonical byte string fed to HMAC. Each variable-
// length field is prefixed by a 4-byte big-endian length so the encoding is
// unambiguous across field boundaries (preventing the "AB|CD" == "A|BCD"
// collision class). The timestamp is a fixed-width int64 of unix nanos.
func encodeClaim(c AuthClaim) []byte {
	out := make([]byte, 0, 4+len(c.NodeID)+4+len(c.Nonce)+8)
	var lenBuf [4]byte

	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(c.NodeID)))
	out = append(out, lenBuf[:]...)
	out = append(out, c.NodeID...)

	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(c.Nonce)))
	out = append(out, lenBuf[:]...)
	out = append(out, c.Nonce...)

	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(c.Timestamp.UnixNano()))
	out = append(out, tsBuf[:]...)

	return out
}

// ComputeAuth returns HMAC-SHA256(secret, canonical(claim)). The output is
// 32 bytes; callers should wire it as-is into the auth operation payload.
func ComputeAuth(secret []byte, claim AuthClaim) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write(encodeClaim(claim))
	return h.Sum(nil)
}

// VerifyAuth checks every property the receiver cares about, in priority order:
// (1) the claim's declared NodeID is the one we expect for this connection;
// (2) the nonce is at least NonceLen bytes (forces the prover to use a real
// challenge rather than a fixed token); (3) the timestamp is recent enough;
// (4) the HMAC matches under the shared secret. Constant-time MAC comparison.
func VerifyAuth(secret []byte, expectedNodeID string, claim AuthClaim, mac []byte, now time.Time) error {
	if claim.NodeID != expectedNodeID {
		return ErrAuthNodeMismatch
	}
	if len(claim.Nonce) < NonceLen {
		return ErrAuthShortNonce
	}
	if now.Sub(claim.Timestamp) > FreshnessWindow {
		return ErrAuthStale
	}
	if claim.Timestamp.Sub(now) > FutureSkew {
		return ErrAuthFuture
	}
	expected := ComputeAuth(secret, claim)
	if !hmac.Equal(expected, mac) {
		return ErrAuthBadMAC
	}
	return nil
}

// NewNonce returns NonceLen cryptographically-random bytes for use as an auth
// challenge. The receiver of a challenge calls this; the prover never does.
func NewNonce() ([]byte, error) {
	buf := make([]byte, NonceLen)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// NewSharedSecret returns 32 cryptographically-random bytes hex-encoded as
// 64 characters. Suitable for use as a peer_orchestrators.shared_secret
// value and for embedding in an invite payload.
func NewSharedSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
