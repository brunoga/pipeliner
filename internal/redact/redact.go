// Package redact masks credentials embedded in URLs before text is written
// somewhere durable or shared, such as the log file or the failure log.
//
// Indexer and tracker URLs carry their secret in the URL itself: Jackett's
// apikey and jackett_apikey query parameters, a private tracker's passkey in
// the announce path, a user:password pair in the authority. Errors from the
// HTTP client quote the whole URL, so the secret ends up in any log line that
// reports the failure. String rewrites those parts to Mask and leaves the rest
// of the URL intact, so the host and path still say which request failed.
package redact

import "regexp"

// Mask replaces each redacted secret.
const Mask = "REDACTED"

// sensitiveParam is a query parameter name that holds a credential: apikey,
// jackett_apikey, passkey, authkey, torrent_pass, access_token, sig and the
// like. Matching on the suffix catches the vendor-prefixed spellings.
const sensitiveParam = `[a-z0-9_.\-]*(?:key|pass|passwd|password|token|secret|auth|sig|signature)`

var rules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// ?apikey=VALUE and &passkey=VALUE.
	{regexp.MustCompile(`(?i)([?&]` + sensitiveParam + `=)[^&#\s"'<>\\]+`), "${1}" + Mask},
	// The same parameter percent-encoded inside another URL's parameter:
	// %3Fapikey%3DVALUE or %26apikey%3DVALUE.
	{regexp.MustCompile(`(?i)((?:%3F|%26)` + sensitiveParam + `%3D)[^&#\s"'<>\\%]+`), "${1}" + Mask},
	// scheme://user:password@host.
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*://[^/\s:@"'<>]*:)[^@/\s"'<>]+@`), "${1}" + Mask + "@"},
	// Passkeys in announce paths: /announce/PASSKEY and /PASSKEY/announce.
	{regexp.MustCompile(`(?i)(/announce(?:\.php)?/)[0-9a-z]{16,}`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)/[0-9a-z]{16,}(/announce)`), "/" + Mask + "${1}"},
}

// String returns s with every credential it can recognise in a URL replaced
// by Mask. Text without a URL-shaped secret is returned unchanged.
func String(s string) string {
	if !mayHoldSecret(s) {
		return s
	}
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Bytes is String for a byte slice. It returns b itself when nothing needed
// masking.
func Bytes(b []byte) []byte {
	if !mayHoldSecret(string(b)) {
		return b
	}
	for _, r := range rules {
		b = r.re.ReplaceAll(b, []byte(r.repl))
	}
	return b
}

// mayHoldSecret is a cheap prefilter so ordinary log lines skip the regular
// expressions: every rule needs a query string, an escaped one, an authority
// or an announce path.
func mayHoldSecret(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '?', '&', '%', '@':
			return true
		case '/':
			if i+1 < len(s) && s[i+1] == '/' {
				return true
			}
			if i+9 <= len(s) && (s[i+1:i+9] == "announce" || s[i+1:i+9] == "ANNOUNCE") {
				return true
			}
		}
	}
	return false
}
