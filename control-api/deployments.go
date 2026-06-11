package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const deploymentsFileName = "deployments.jsonl"

var reDeployTarget = regexp.MustCompile(`^[a-zA-Z0-9:._-]{0,160}$`)

type deploymentRecord struct {
	Time       string         `json:"time"`
	Mode       string         `json:"mode"`
	Target     string         `json:"target,omitempty"`
	Commit     string         `json:"commit,omitempty"`
	Generation int            `json:"generation,omitempty"`
	Result     string         `json:"result"`
	Apps       map[string]any `json:"apps,omitempty"`
}

func currentCommit(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func deploymentNeedsConfirm(mode string) bool {
	return mode == "switch" || mode == "rollback"
}

func validDeploymentMode(mode string) bool {
	switch mode {
	case "dry-run", "build", "switch", "rollback":
		return true
	default:
		return false
	}
}

func readDeploymentHistory(limit int) []deploymentRecord {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f, err := os.Open(statePath(deploymentsFileName))
	if err != nil {
		return []deploymentRecord{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	items := []deploymentRecord{}
	for sc.Scan() {
		var rec deploymentRecord
		if json.Unmarshal(sc.Bytes(), &rec) == nil {
			items = append(items, rec)
		}
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func listDeployJobs() []map[string]string {
	out, err := runRead(10*time.Second, systemctl, "list-units", "ci-deploy", "hl-deploy@*", "hl-backup@*", "hl-apply", "--all", "--no-legend", "--plain")
	if err != nil {
		return []map[string]string{}
	}
	jobs := []map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 4 {
			jobs = append(jobs, map[string]string{"unit": f[0], "load": f[1], "active": f[2], "sub": f[3]})
		}
	}
	return jobs
}

func deploymentLogs(unit string) string {
	if !regexp.MustCompile(`^(ci-deploy\.service|hl-apply\.service|hl-(deploy|backup)@[0-9a-z-]+\.service)$`).MatchString(unit) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", "80", "--no-pager").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return string(out)
}

func startDeploymentJob(w http.ResponseWriter, r *http.Request, mode, target string) {
	if !validDeploymentMode(mode) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad deployment mode"})
		return
	}
	if !reDeployTarget.MatchString(target) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad deployment target"})
		return
	}
	dir := homelabDir()
	jobID := time.Now().UTC().Format("20060102-150405") + "-" + randomID(8)
	unit := "hl-deploy@" + jobID + ".service"
	if strings.HasPrefix(target, "app:") && mode == "rollback" {
		writeJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "direct app rollback is disabled; use /v1/changes/app-rollback"})
		return
	}
	if err := writeJobSpec(jobID, mode, target); err != nil {
		appendAuditEvent(r, auditEvent{Op: "deployment", Kind: "deploy", Target: target, Risk: "risky", Result: "failed", Status: http.StatusInternalServerError, JobID: jobID, Error: err.Error()})
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	out, err := exec.Command(systemctl, "start", "--no-block", unit).CombinedOutput()
	if err != nil {
		appendAuditEvent(r, auditEvent{Op: "deployment", Kind: "deploy", Target: target, Risk: "risky", Result: "failed", Status: http.StatusInternalServerError, JobID: jobID, Commit: currentCommit(dir), Error: err.Error()})
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "output": string(out), "error": err.Error()})
		return
	}
	risk := "safe"
	if deploymentNeedsConfirm(mode) {
		risk = "risky"
	}
	appendAuditEvent(r, auditEvent{Op: mode, Kind: "deployment", Target: target, Risk: risk, Result: "started", Status: http.StatusOK, JobID: jobID, Commit: currentCommit(dir)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID, "unit": unit, "output": "deployment started"})
}

func deploymentsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Deploy history and unit journals can leak commit IDs, repo URLs and
		// failure output — operator only, like the other log endpoints.
		if !requireRole(w, r, "operator") {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		unit := r.URL.Query().Get("unit")
		res := map[string]any{
			"history":    readDeploymentHistory(limit),
			"jobs":       listDeployJobs(),
			"generation": currentGeneration(),
			"deploy":     deployState(),
		}
		if unit != "" {
			res["logs"] = deploymentLogs(unit)
		}
		writeJSON(w, http.StatusOK, res)
	case http.MethodPost:
		var req struct {
			Mode      string `json:"mode"`
			Target    string `json:"target"`
			ConfirmID string `json:"confirm_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
			return
		}
		if req.Mode == "" {
			req.Mode = "switch"
		}
		minRole := "operator"
		if req.Mode == "switch" || req.Mode == "rollback" {
			minRole = "admin"
		}
		if !requireRole(w, r, minRole) {
			return
		}
		if deploymentNeedsConfirm(req.Mode) {
			msg := "Confirm " + req.Mode
			if req.Target != "" {
				msg += " " + req.Target
			}
			if !requireDoubleConfirm(w, r, confirmKey("deployment", req.Mode, req.Target), msg, req.ConfirmID) {
				appendAuditEvent(r, auditEvent{Op: req.Mode, Kind: "deployment", Target: req.Target, Risk: "risky", Result: "armed", Status: http.StatusConflict})
				return
			}
		}
		startDeploymentJob(w, r, req.Mode, req.Target)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}
