package platform

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// mxTimeout bounds the DNS lookup. Registration must not hang on a slow or
// unreachable resolver, and the check is an improvement on accepting anything,
// never a gate the whole flow depends on.
const mxTimeout = 3 * time.Second

var errEmailFormat = errors.New("provide a valid email address")
var errEmailDomain = errors.New("that domain cannot receive mail; check the spelling")

// normalizeEmail validates the shape of an address and returns it in the form it
// will be stored in. The local part keeps its case, because only the domain is
// case-insensitive by specification; lowercasing the whole address would merge
// two addresses that a strict mail server treats as different.
func normalizeEmail(raw string) (string, error) {
	address := strings.TrimSpace(raw)
	if address == "" {
		return "", nil // absent is allowed: an email is never required here
	}
	if len(address) > 254 || strings.ContainsAny(address, " \t\r\n\"'<>,;\\") {
		return "", errEmailFormat
	}
	at := strings.LastIndex(address, "@")
	if at < 1 || at == len(address)-1 {
		return "", errEmailFormat
	}
	local, domain := address[:at], strings.ToLower(address[at+1:])
	if len(local) > 64 || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return "", errEmailFormat
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", errEmailFormat
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errEmailFormat
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", errEmailFormat
			}
		}
	}
	return local + "@" + domain, nil
}

// deliverable reports whether the domain publishes a way to receive mail.
//
// It distinguishes a definite answer from no answer at all. A domain that
// resolves but announces no mail host is rejected, which is what catches a typo
// like gmial.com. A lookup that fails because there is no resolver - an offline
// machine, an air-gapped deployment - returns false with no error, and the
// caller accepts the address unchecked. Refusing registrations because a DNS
// server is unreachable would trade a real capability for a cosmetic one.
func deliverable(ctx context.Context, domain string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, mxTimeout)
	defer cancel()
	var resolver net.Resolver
	records, err := resolver.LookupMX(ctx, domain)
	if err == nil {
		for _, record := range records {
			if strings.TrimSuffix(record.Host, ".") != "" {
				return true, nil
			}
		}
		// Resolved cleanly with no usable mail host: definitively undeliverable.
		return false, errEmailDomain
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		// The domain itself does not exist.
		return false, errEmailDomain
	}
	if errors.As(err, &dnsErr) && !dnsErr.IsTemporary && !dnsErr.IsTimeout {
		// Some resolvers report "no MX records" as a non-temporary error rather
		// than an empty answer. Fall back to asking whether the host resolves at
		// all, since a domain with an A record can still accept mail.
		if addrs, hostErr := resolver.LookupHost(ctx, domain); hostErr == nil && len(addrs) > 0 {
			return true, nil
		}
		return false, errEmailDomain
	}
	return false, nil // inconclusive: accept, but do not record it as checked
}

// checkEmail normalizes an address and, when possible, confirms its domain can
// receive mail. The returned time is non-zero only when the check actually
// happened, so a stored timestamp never implies a check that did not run.
func checkEmail(ctx context.Context, raw string) (string, time.Time, error) {
	address, err := normalizeEmail(raw)
	if err != nil || address == "" {
		return "", time.Time{}, err
	}
	ok, err := deliverable(ctx, address[strings.LastIndex(address, "@")+1:])
	if err != nil {
		return "", time.Time{}, err
	}
	if ok {
		return address, time.Now().UTC(), nil
	}
	return address, time.Time{}, nil
}
