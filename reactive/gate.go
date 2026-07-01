// Package reactive turns events on the domain-event spine into agent
// cognition: it subscribes to the bus, filters events through a salience gate
// and loop guard, and wakes the matching agent via the interpreter. See
// docs/reactive-agents-design.md.
package reactive

import (
	"fmt"
	"path"
	"strings"

	"github.com/everydev1618/govega/events"
)

// passRules is tier 1 of the salience gate (§5.1): a free, synchronous check
// that an event both matches a trigger's type pattern and satisfies its
// optional predicate. Type mismatch short-circuits before the predicate.
func passRules(on, where string, e events.Event) (bool, error) {
	if !matchType(on, e.Type) {
		return false, nil
	}
	return evalWhere(where, e.Data)
}

// matchType reports whether an event type matches a glob pattern such as
// "agent.*", "agent.completed", or "*". The '.' in event types is an ordinary
// character; '*' matches any run of them.
func matchType(pattern, typ string) bool {
	ok, err := path.Match(pattern, typ)
	return err == nil && ok
}

// evalWhere evaluates a single-clause predicate over an event's Data. Supported
// operators: ==, !=, ~= (substring), and " contains ". An empty clause always
// passes. Values are compared by their string form; a missing key reads as "".
// This is deliberately tiny — a richer language is a non-goal for v1.
func evalWhere(where string, data map[string]any) (bool, error) {
	where = strings.TrimSpace(where)
	if where == "" {
		return true, nil
	}

	op, idx, oplen := parseOp(where)
	if op == "" {
		return false, fmt.Errorf("reactive: malformed where clause %q (no operator)", where)
	}

	key := strings.TrimSpace(where[:idx])
	rhs := unquote(strings.TrimSpace(where[idx+oplen:]))

	lhs := ""
	if v, ok := data[key]; ok {
		lhs = fmt.Sprint(v)
	}

	switch op {
	case "==":
		return lhs == rhs, nil
	case "!=":
		return lhs != rhs, nil
	case "~=", "contains":
		return strings.Contains(lhs, rhs), nil
	}
	return false, nil
}

// parseOp finds the operator in a where clause and returns it with its byte
// offset and length. Order matters: "==" is checked before "!=" and "~=" so
// the shared '=' does not cause a misparse.
func parseOp(where string) (op string, idx, oplen int) {
	if i := strings.Index(where, "=="); i >= 0 {
		return "==", i, 2
	}
	if i := strings.Index(where, "!="); i >= 0 {
		return "!=", i, 2
	}
	if i := strings.Index(where, "~="); i >= 0 {
		return "~=", i, 2
	}
	if i := strings.Index(where, " contains "); i >= 0 {
		return "contains", i, len(" contains ")
	}
	return "", -1, 0
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
