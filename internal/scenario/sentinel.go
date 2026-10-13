package scenario

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// sentinelImage writes and reads the sentinel files. Pinned by digest.
const sentinelImage = "busybox:1.36.1@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"

// sentinelFile is where the sentinel goes inside each volume. The name says who wrote it, in case
// someone finds it in their own data.
const sentinelFile = "/pvc/.helmbulance-sentinel"

// newSentinel returns a random value, so a volume that was emptied and refilled cannot match.
func newSentinel() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeSentinelScript writes value into the volume and prints "written", or "write-failed" when
// the volume is read-only or full.
func writeSentinelScript(value string) string {
	return "if printf %s " + value + " > " + sentinelFile + "; then echo written; else echo write-failed; fi"
}

// readSentinelScript prints the sentinel, or "missing".
const readSentinelScript = "cat " + sentinelFile + " 2>/dev/null || echo missing"

// sentinelState compares what the read pod printed with the value written.
func sentinelState(out, want string) string {
	switch got := strings.TrimSpace(out); got {
	case want:
		return "kept"
	case "missing", "":
		return "missing"
	default:
		return "replaced"
	}
}
