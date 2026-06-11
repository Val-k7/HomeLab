package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Per-actor token bucket on mutations: 1 request/second refill, burst 30.
// Far above anything a human operator produces, but it bounds the damage of a
// misbehaving client looping on mutations (audit-log disk fill, nix-eval CPU).
// Disable with CONTROL_API_RATE_LIMIT=off (used by load tests).
const (
	mutationRefillPerSec = 1.0
	mutationBurst        = 30.0
)

var (
	rateMu      sync.Mutex
	rateBuckets = map[string]*rateBucket{}
	rateNow     = time.Now // overridable in tests
)

type rateBucket struct {
	tokens float64
	last   time.Time
}

func allowMutation(actor string) bool {
	if os.Getenv("CONTROL_API_RATE_LIMIT") == "off" {
		return true
	}
	now := rateNow()
	rateMu.Lock()
	defer rateMu.Unlock()
	// Prune buckets idle long enough to be full again — keeps the map bounded
	// over the daemon's lifetime.
	for k, old := range rateBuckets {
		if now.Sub(old.last).Seconds()*mutationRefillPerSec >= mutationBurst {
			delete(rateBuckets, k)
		}
	}
	b, ok := rateBuckets[actor]
	if !ok {
		b = &rateBucket{tokens: mutationBurst, last: now}
		rateBuckets[actor] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * mutationRefillPerSec
	if b.tokens > mutationBurst {
		b.tokens = mutationBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

var roleLevel = map[string]int{
	"viewer":     0,
	"operator":   1,
	"maintainer": 2,
	"admin":      3,
}

type accessConfig struct {
	DefaultRole string            `json:"default_role"`
	Users       map[string]string `json:"users"`
}

func accessFilePath() string {
	if p := os.Getenv("HOMELAB_ACCESS_FILE"); p != "" {
		return p
	}
	return "/etc/homelab/access.json"
}

func readAccessConfig() accessConfig {
	cfg := accessConfig{DefaultRole: "viewer", Users: map[string]string{}}
	b, err := os.ReadFile(accessFilePath())
	if err != nil {
		// A missing file is a legitimate "defaults only" setup, but any other
		// error (e.g. permission denied) silently downgrades every user to the
		// default role — make that visible.
		if !os.IsNotExist(err) {
			log.Printf("warn: readAccessConfig: %v (falling back to default_role)", err)
		}
		return cfg
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Printf("warn: readAccessConfig: bad %s: %v", accessFilePath(), err)
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "viewer"
	}
	if cfg.Users == nil {
		cfg.Users = map[string]string{}
	}
	return cfg
}

func normalizedRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if _, ok := roleLevel[role]; ok {
		return role
	}
	return "viewer"
}

func roleFromRequest(r *http.Request) string {
	// Role is derived ONLY from the authenticated identity that oauth2-proxy
	// injects — never from a client-supplied role header (those are spoofable
	// since oauth2-proxy does not strip arbitrary request headers).
	cfg := readAccessConfig()
	actor := actorFromRequest(r)
	if role, ok := cfg.Users[actor]; ok {
		return normalizedRole(role)
	}
	return normalizedRole(cfg.DefaultRole)
}

// isLoopbackRequest reports whether the request reached us over loopback. In
// production control-api binds 127.0.0.1 only, so every legitimate request
// arrives via the local oauth2-proxy (already authenticated).
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireMutationAuth confirms the caller crossed the authentication boundary:
// the local oauth2-proxy (loopback). The role (authorization) is then enforced
// separately by requireRole.
func requireMutationAuth(w http.ResponseWriter, r *http.Request) bool {
	if !isLoopbackRequest(r) {
		appendAuditEvent(r, auditEvent{Op: "auth", Result: "blocked", Status: http.StatusForbidden, Error: "request did not cross the auth proxy"})
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "unauthenticated"})
		return false
	}
	if r.Header.Get("X-HL-CSRF") == "" {
		appendAuditEvent(r, auditEvent{Op: "auth", Result: "blocked", Status: http.StatusForbidden, Error: "missing CSRF header"})
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "missing X-HL-CSRF header"})
		return false
	}
	return rateLimitOK(w, r, rateKeyFromRequest(r))
}

// rateKeyFromRequest picks the rate-limit bucket key. actorFromRequest falls
// back to RemoteAddr, whose ephemeral port would give every new TCP connection
// a fresh bucket (bypassing the budget and growing the map per connection), so
// anonymous loopback callers share a single fixed bucket instead.
func rateKeyFromRequest(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-Email"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-User"); v != "" {
		return v
	}
	return "loopback-anonymous"
}

// rateLimitOK enforces the per-actor mutation budget after authentication
// succeeded. A 429 is audited once per rejected request.
func rateLimitOK(w http.ResponseWriter, r *http.Request, actor string) bool {
	if allowMutation(actor) {
		return true
	}
	appendAuditEvent(r, auditEvent{Op: "auth", Result: "blocked", Status: http.StatusTooManyRequests, Error: "mutation rate limit exceeded"})
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "rate limit exceeded, retry later"})
	return false
}

func requireRole(w http.ResponseWriter, r *http.Request, minRole string) bool {
	if !requireMutationAuth(w, r) {
		return false
	}
	got := roleFromRequest(r)
	if roleLevel[got] >= roleLevel[normalizedRole(minRole)] {
		return true
	}
	appendAuditEvent(r, auditEvent{Op: "authz", Result: "blocked", Status: http.StatusForbidden, Error: "insufficient role: " + got})
	writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "insufficient role", "role": got, "required": minRole})
	return false
}
