package llm

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// sensitiveHeaders are request headers whose values are credentials. Compared
// lowercased, since a header set directly on the map skips canonicalization.
var sensitiveHeaders = map[string]bool{
	"x-api-key":           true,
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"x-auth-token":        true,
}

// redactHeaders renders request headers for a log line with credential values
// masked. The header name is kept so a missing or malformed key is still
// diagnosable; only the value is withheld.
func redactHeaders(h http.Header) string {
	parts := make([]string, 0, len(h))
	for name, values := range h {
		if sensitiveHeaders[strings.ToLower(name)] {
			parts = append(parts, name+":[REDACTED]")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%v", name, values))
	}
	// Stable order so log lines are comparable between requests.
	sort.Strings(parts)
	return "map[" + strings.Join(parts, " ") + "]"
}
