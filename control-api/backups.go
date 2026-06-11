package main

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// backups.go exposes backup STATUS (read-only) and audited runtime backup
// ACTIONS (backup now, restore test, verify, snapshots, restore to temp).
// Backup jobs write their results to backups.json in the state dir; the
// status view joins that with the manifest's backed-up volumes for coverage.

var reBackupApp = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

const backupsStateFile = "backups.json"

// backupResult is one app's last backup/restore record, written by bin/backup.sh.
type backupResult struct {
	App             string `json:"app"`
	LastBackup      string `json:"last_backup,omitempty"`
	LastRestoreTest string `json:"last_restore_test,omitempty"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	DurationSec     int    `json:"duration_sec,omitempty"`
	Snapshots       int    `json:"snapshots,omitempty"`
	Error           string `json:"error,omitempty"`
}

func loadBackupResults() map[string]backupResult {
	res := map[string]backupResult{}
	if b, err := os.ReadFile(statePath(backupsStateFile)); err == nil {
		_ = json.Unmarshal(b, &res)
	}
	return res
}

// backupCoverage joins manifest apps with backup results. Pure for testing.
func backupCoverage(apps map[string]ManifestApp, pol Policies, results map[string]backupResult) []map[string]any {
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)

	out := []map[string]any{}
	for _, name := range names {
		app := apps[name]
		hasBackedUp := false
		for _, v := range app.Volumes {
			if v.BackedUp {
				hasBackedUp = true
			}
		}
		required := false
		restoreTest := false
		if req, ok := pol.BackupByCriticality[app.Criticality]; ok {
			required = req.Required
			restoreTest = req.RestoreTest
		}
		covered := !required || hasBackedUp
		r := results[name]
		out = append(out, map[string]any{
			"app":               name,
			"criticality":       app.Criticality,
			"backup_required":   required,
			"restore_required":  restoreTest,
			"has_backed_up":     hasBackedUp,
			"covered":           covered,
			"last_backup":       r.LastBackup,
			"last_restore_test": r.LastRestoreTest,
			"size_bytes":        r.SizeBytes,
			"duration_sec":      r.DurationSec,
			"snapshots":         r.Snapshots,
			"error":             r.Error,
		})
	}
	return out
}

func backupsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	coverage := backupCoverage(loadManifestApps(), loadPolicies(), loadBackupResults())
	uncovered := 0
	for _, c := range coverage {
		if cov, ok := c["covered"].(bool); ok && !cov {
			uncovered++
		}
	}
	// Backups only actually run with a repository URL AND the restic password
	// secret on the host; surface that so the UI shows a setup CTA instead of
	// a green "100% coverage" on an unconfigured system.
	repoSet := strings.TrimSpace(loadPlatform().Backup.Repository) != ""
	pwSet := false
	if _, err := os.Stat("/run/secrets/restic_password"); err == nil || (err != nil && !os.IsNotExist(err)) {
		pwSet = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apps": coverage, "uncovered": uncovered,
		"configured": repoSet && pwSet, "repository_set": repoSet, "password_set": pwSet,
	})
}

// backupsLogsHandler returns recent journald output for the backup job units
// (hl-backup@*), so the operator can read why a backup/restore failed without
// leaving the UI. Read-only, operator role.
func backupsLogsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	out, err := runRead(10*time.Second, "journalctl", "-u", "hl-backup@*", "-n", "200", "--no-pager", "--output=short-iso")
	if err != nil && len(out) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "journalctl failed", "logs": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": "hl-backup@*", "logs": string(out)})
}

// backupActionOp maps an action endpoint to the bin/backup.sh verb.
var backupActionOps = map[string]string{
	"run":          "backup",
	"restore-test": "restore-test",
	"verify":       "verify",
	"snapshots":    "snapshots",
	"restore":      "restore",
}

func backupActionHandler(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if !requireRole(w, r, "maintainer") {
			return
		}
		var req struct {
			App       string `json:"app"`
			Snapshot  string `json:"snapshot"`
			ConfirmID string `json:"confirm_id"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.App != "" && !reBackupApp.MatchString(req.App) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad app"})
			return
		}
		// Optional restic snapshot id for restore/restore-test ("latest" when
		// empty). Short or full hex ids only.
		if req.Snapshot != "" && req.Snapshot != "latest" && !regexp.MustCompile(`^[a-f0-9]{8,64}$`).MatchString(req.Snapshot) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad snapshot id"})
			return
		}
		// Destructive verbs must name a single app — an empty app would fan the
		// operation out across the whole fleet.
		if (op == "restore" || op == "restore-test") && req.App == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "app is required for " + op})
			return
		}
		// restore overwrites live volume data — gate it behind a double-confirm,
		// like host reboot and deploy switch.
		if op == "restore" {
			if !requireDoubleConfirm(w, r, confirmKey("backup", "restore", req.App), "Confirm restore "+req.App, req.ConfirmID) {
				appendAuditEvent(r, auditEvent{Op: "backup.restore", Kind: "backup", Target: req.App, Risk: "risky", Result: "armed", Status: http.StatusConflict})
				return
			}
		}
		verb := backupActionOps[op]
		jobID := time.Now().UTC().Format("20060102-150405") + "-" + randomID(8)
		unit := "hl-backup@" + jobID + ".service"
		if err := writeJobSpec(jobID, verb, req.App, req.Snapshot); err != nil {
			appendAuditEvent(r, auditEvent{Op: "backup." + op, Kind: "backup", Target: req.App, Risk: "risky", Result: "failed", Status: http.StatusInternalServerError, JobID: jobID, Error: err.Error()})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		res := commandRunner(nil, systemctl, "start", "--no-block", unit)
		if res.Err != nil {
			appendAuditEvent(r, auditEvent{Op: "backup." + op, Kind: "backup", Target: req.App, Risk: "risky", Result: "failed", Status: http.StatusInternalServerError, JobID: jobID, Error: res.Err.Error()})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "output": res.Output, "error": res.Err.Error()})
			return
		}
		appendAuditEvent(r, auditEvent{Op: "backup." + op, Kind: "backup", Target: req.App, Risk: "risky", Result: "started", Status: http.StatusOK, JobID: jobID})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID, "unit": unit, "op": op})
	}
}
