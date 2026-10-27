package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// A request is signed over its method, path, timestamp (whole
// seconds) and body, and ed25519 signs deterministically: two
// identical requests in one second carry one signature. The site
// accepts each signature once (its replay guard), so the second was
// refused as already received; the overlay's send asked for the
// channel list twice in a row and hit it every time. uniqueStamp
// stamps a repeat in the same second a second later, well inside the
// site's five-minute clock allowance, so every request is fresh.

var stamps struct {
	mu   sync.Mutex
	last map[string]int64
}

// uniqueStamp is the timestamp to sign a request with: now, or one
// past the last stamp an identical request (key) was signed with.
func uniqueStamp(key string, now int64) int64 {
	stamps.mu.Lock()
	defer stamps.mu.Unlock()
	if stamps.last == nil {
		stamps.last = map[string]int64{}
	}
	if last, ok := stamps.last[key]; ok && now <= last {
		now = last + 1
	}
	stamps.last[key] = now
	if len(stamps.last) > 1024 {
		for k, t := range stamps.last {
			if t < now-600 {
				delete(stamps.last, k)
			}
		}
	}
	return now
}

func bodyHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}
