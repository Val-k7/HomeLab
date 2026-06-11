package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultStateDir = "/var/lib/homelab"
	auditFileName   = "audit.jsonl"
	auditMaxBytes   = 5 << 20 // 5 MiB; rotate audit.jsonl past this size
	confirmTTL      = 10 * time.Second
)

type auditEvent struct {
	Time       string `json:"time"`
	Actor      string `json:"actor"`
	Op         string `json:"op"`
	Kind       string `json:"kind,omitempty"`
	Target     string `json:"target,omitempty"`
	Risk       string `json:"risk,omitempty"`
	Result     string `json:"result"`
	Status     int    `json:"status,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Generation int    `json:"generation,omitempty"`
	Error      string `json:"error,omitempty"`
	Message    string `json:"message,omitempty"`
}

type confirmChallenge struct {
	Key     string
	Message string
	Expires time.Time
}

var (
	confirmMu sync.Mutex
	confirms  = map[string]confirmChallenge{}
)

func stateDir() string {
	if d := os.Getenv("HOMELAB_STATE_DIR"); d != "" {
		return d
	}
	return defaultStateDir
}

func statePath(name string) string {
	return filepath.Join(stateDir(), name)
}

// writeJobSpec writes a deploy/backup job spec read by the hl-*@.service template
// run-scripts. One argument per line (verb, app, then optional extras like a
// snapshot id; any line may be empty). 0600 under the state dir.
func writeJobSpec(jobID string, lines ...string) error {
	dir := filepath.Join(stateDir(), "jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, jobID+".json"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func ensureStateDir() error {
	return os.MkdirAll(stateDir(), 0o755)
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// IDs feed confirm tokens and change/job identifiers; a predictable
		// fallback would be guessable. Refuse to continue without entropy.
		panic("randomID: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func actorFromRequest(r *http.Request) string {
	if r == nil {
		return "system"
	}
	// Only headers oauth2-proxy sets (and overwrites) are trusted as identity.
	// X-Remote-User / X-Grafana-User are not set by oauth2-proxy and would be
	// client-spoofable, so they are intentionally not consulted.
	for _, h := range []string{"X-Forwarded-Email", "X-Forwarded-User"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func appendAuditEvent(r *http.Request, ev auditEvent) {
	if ev.Time == "" {
		ev.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if ev.Actor == "" {
		ev.Actor = actorFromRequest(r)
	}
	if ev.Result == "" {
		ev.Result = "info"
	}
	if ev.Generation == 0 {
		ev.Generation = currentGeneration()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	// Tamper-evidence mirror: the file is the operator-readable history, the
	// systemd journal (stdout via journald) is the tamper-evident copy. An
	// attacker with the controlapi uid can truncate the file but not the
	// root-owned, append-only journal.
	log.Printf("HL_AUDIT %s", b)
	if ensureStateDir() != nil {
		return
	}
	rotateAuditIfNeeded()
	f, err := os.OpenFile(statePath(auditFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("audit append failed: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		log.Printf("audit write failed: %v", err)
	}
}

// rotateAuditIfNeeded renames audit.jsonl to audit.jsonl.1 (replacing any
// existing .1) once the current file exceeds auditMaxBytes. Errors are logged
// but never propagated so they cannot break the request path.
func rotateAuditIfNeeded() {
	cur := statePath(auditFileName)
	fi, err := os.Stat(cur)
	if err != nil {
		// Missing file (nothing to rotate) or stat error: nothing to do.
		return
	}
	if fi.Size() < auditMaxBytes {
		return
	}
	if err := os.Rename(cur, cur+".1"); err != nil {
		log.Printf("audit rotate failed: %v", err)
	}
}

type auditQuery struct {
	Limit     int
	IncludeUI bool
	Op        string
	Kind      string
	Result    string
}

func readAuditEvents(q auditQuery) []auditEvent {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Read the current file (oldest-first within the file).
	events := scanAuditFile(statePath(auditFileName), q)
	// If a rotation happened and we don't yet have a full page, backfill from
	// the rotated file so history survives the rotation. The rotated file is
	// strictly older, so its events are prepended (oldest -> newest order).
	if len(events) < limit {
		if rotated := scanAuditFile(statePath(auditFileName)+".1", q); len(rotated) > 0 {
			events = append(rotated, events...)
		}
	}
	if len(events) <= limit {
		reverseAudit(events)
		return events
	}
	events = events[len(events)-limit:]
	reverseAudit(events)
	return events
}

// scanAuditFile reads one audit file and returns its matching events in
// file order (oldest first). A missing file yields an empty slice.
func scanAuditFile(path string, q auditQuery) []auditEvent {
	f, err := os.Open(path)
	if err != nil {
		return []auditEvent{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	events := []auditEvent{}
	for sc.Scan() {
		var ev auditEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			if !q.IncludeUI && ev.Kind == "ui" {
				continue
			}
			if q.Op != "" && ev.Op != q.Op {
				continue
			}
			if q.Kind != "" && ev.Kind != q.Kind {
				continue
			}
			if q.Result != "" && ev.Result != q.Result {
				continue
			}
			events = append(events, ev)
		}
	}
	return events
}

func auditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	// The audit log exposes actors, targets, and deploy/secret/PR history; gate
	// it to operator+ rather than letting a default viewer read it.
	if !requireRole(w, r, "operator") {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	q := auditQuery{
		Limit:     limit,
		IncludeUI: r.URL.Query().Get("include_ui") == "true",
		Op:        r.URL.Query().Get("op"),
		Kind:      r.URL.Query().Get("kind"),
		Result:    r.URL.Query().Get("result"),
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": readAuditEvents(q)})
}

func reverseAudit(events []auditEvent) {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
}

func confirmKey(parts ...string) string {
	return filepath.Join(parts...)
}

func requireDoubleConfirm(w http.ResponseWriter, r *http.Request, key, message, confirmID string) bool {
	now := time.Now()
	confirmMu.Lock()
	defer confirmMu.Unlock()
	for id, c := range confirms {
		if now.After(c.Expires) {
			delete(confirms, id)
		}
	}
	if confirmID != "" {
		if c, ok := confirms[confirmID]; ok && c.Key == key && now.Before(c.Expires) {
			delete(confirms, confirmID)
			return true
		}
	}
	id := randomID(16)
	confirms[id] = confirmChallenge{
		Key:     key,
		Message: message,
		Expires: now.Add(confirmTTL),
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"ok":         false,
		"confirm":    "double",
		"confirm_id": id,
		"expires_in": int(confirmTTL.Seconds()),
		"message":    message,
	})
	return false
}
