package serve

import "testing"

func TestUserAllowed(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		userID  string
		want    bool
	}{
		{"empty allowlist is open", nil, "123", true},
		{"empty slice is open", []string{}, "123", true},
		{"listed user allowed", []string{"123", "456"}, "123", true},
		{"unlisted user denied", []string{"123", "456"}, "999", false},
		{"whitespace trimmed", []string{" 123 "}, "123", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := userAllowed(newAllowSet(c.allowed), c.userID); got != c.want {
				t.Errorf("userAllowed(%v, %q) = %v, want %v", c.allowed, c.userID, got, c.want)
			}
		})
	}
}
