package oracle

import (
	"path"
	"strings"
)

// Key classes for Secret drift (D5 in docs/decisions/0001-v0.1-path.md).
const (
	// ClassCredential is a password, token or key that data or a session depends on.
	ClassCredential = "credential"
	// ClassTLS is a certificate or its private key. Charts often regenerate these on purpose
	// (genCA), and clients that pin the old CA are the ones that break.
	ClassTLS = "tls"
	// ClassOther is any other key.
	ClassOther = "other"
)

// tlsNames are the key names Kubernetes and most charts use for certificates.
var tlsNames = map[string]bool{"tls.crt": true, "tls.key": true, "ca.crt": true, "ca.key": true}

// credentialWords mark a key name as a credential or an encryption key.
var credentialWords = []string{"password", "passwd", "secret", "token", "jwt", "salt", "credential", "apikey", "api-key", "api_key", "auth", "key"}

// KeyClass guesses what a Secret key holds from its name alone; values are never read.
func KeyClass(key string) string {
	k := strings.ToLower(key)
	if tlsNames[k] {
		return ClassTLS
	}
	switch path.Ext(k) {
	case ".crt", ".pem", ".cer":
		return ClassTLS
	}
	for _, w := range credentialWords {
		if strings.Contains(k, w) {
			return ClassCredential
		}
	}
	return ClassOther
}
