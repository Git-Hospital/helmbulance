package oracle

import "testing"

func TestKeyClass(t *testing.T) {
	for key, want := range map[string]string{
		"tls.crt": ClassTLS, "tls.key": ClassTLS, "ca.crt": ClassTLS, "server.pem": ClassTLS, "webhook.crt": ClassTLS,
		"password": ClassCredential, "postgres-password": ClassCredential, "admin-password": ClassCredential,
		"jwtSecret": ClassCredential, "encryptionKey": ClassCredential, "storage-key": ClassCredential,
		"haSharedSecret": ClassCredential, "API_TOKEN": ClassCredential, "encryption.key": ClassCredential,
		"username": ClassOther, "database": ClassOther, "config.yaml": ClassOther, "host": ClassOther,
	} {
		if got := KeyClass(key); got != want {
			t.Errorf("KeyClass(%q) = %s, want %s", key, got, want)
		}
	}
}
