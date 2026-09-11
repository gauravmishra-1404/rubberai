package platform

import (
	"context"
	"strings"
	"testing"
)

func TestEmailFormat(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  Gaurav@GMAIL.com ", "Gaurav@gmail.com"}, // domain lowercased, local preserved
		{"a.b+tag@sub.example.co.uk", "a.b+tag@sub.example.co.uk"},
	} {
		got, err := normalizeEmail(c.in)
		if err != nil || got != c.want {
			t.Fatalf("normalizeEmail(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{
		"no-at-sign", "@example.com", "user@", "user@nodot",
		"user name@example.com", "a@b..c", ".lead@example.com", "trail.@example.com",
		"user@-bad.com", "user@bad-.com", "user@exa_mple.com",
		strings.Repeat("x", 65) + "@example.com",
	} {
		if _, err := normalizeEmail(bad); err == nil {
			t.Fatalf("accepted invalid address %q", bad)
		}
	}
}

// Requires DNS. A machine without a resolver reports the address as unchecked
// rather than failing, which is the behaviour this asserts for the real domain.
func TestEmailDeliverability(t *testing.T) {
	ctx := context.Background()
	if _, checked, err := checkEmail(ctx, "someone@gmail.com"); err != nil {
		t.Fatalf("rejected a deliverable domain: %v", err)
	} else if checked.IsZero() {
		t.Skip("no resolver available; deliverability could not be established")
	}
	// A domain reserved by RFC 2606 for exactly this: guaranteed never to exist.
	if _, _, err := checkEmail(ctx, "someone@this-domain-does-not-exist.invalid"); err == nil {
		t.Fatal("accepted an address whose domain cannot receive mail")
	}
}
