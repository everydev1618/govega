package dsl

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// DefaultMaxDelegationDepth bounds how many agent-to-agent delegation hops
// a single originating request may chain. Without a bound, an A→B→A
// dispatch ping-pong (a confused or prompt-injected agent re-delegating)
// burns full LLM turns forever. Override with VEGA_MAX_DELEGATION_DEPTH.
const DefaultMaxDelegationDepth = 8

// delegationChainKey carries the chain of agent names delegated through on
// this request path. It survives DispatchToAgent's context detach
// (context.WithoutCancel preserves values) so async chains are bounded too.
type delegationChainKey struct{}

func maxDelegationDepth() int {
	if v := os.Getenv("VEGA_MAX_DELEGATION_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxDelegationDepth
}

// delegationChainFromContext returns the delegation hops taken so far.
func delegationChainFromContext(ctx context.Context) []string {
	chain, _ := ctx.Value(delegationChainKey{}).([]string)
	return chain
}

// contextWithDelegationHop returns a context whose chain has agentName
// appended. The chain is copied — sibling delegations from the same parent
// must not see each other's hops.
func contextWithDelegationHop(ctx context.Context, agentName string) context.Context {
	prev := delegationChainFromContext(ctx)
	chain := make([]string, 0, len(prev)+1)
	chain = append(chain, prev...)
	chain = append(chain, agentName)
	return context.WithValue(ctx, delegationChainKey{}, chain)
}

// checkDelegation enforces the depth cap and cycle rejection for a
// delegation to agentName. Returns a descriptive error the calling agent
// can act on (the error text becomes its tool result).
func checkDelegation(ctx context.Context, agentName string) error {
	chain := delegationChainFromContext(ctx)
	if max := maxDelegationDepth(); len(chain) >= max {
		return fmt.Errorf("delegation depth limit reached (%d hops: %s): finish the task yourself or report back instead of delegating further",
			max, strings.Join(chain, " → "))
	}
	if slices.Contains(chain, agentName) {
		return fmt.Errorf("delegation cycle: %q is already working in this chain (%s): do not delegate back — post to a shared channel or file an inbox item instead",
			agentName, strings.Join(chain, " → "))
	}
	return nil
}
