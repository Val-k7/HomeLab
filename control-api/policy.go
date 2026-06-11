package main

import "strings"

type actionSpec struct {
	Op            string `json:"op"`
	Label         string `json:"label"`
	Risk          string `json:"risk"`
	Enabled       bool   `json:"enabled"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	Confirm       string `json:"confirm,omitempty"`
}

type targetInfo struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name,omitempty"`
	Target      string       `json:"target"`
	State       string       `json:"state"`
	Sub         string       `json:"sub,omitempty"`
	Runner      string       `json:"runner,omitempty"`
	Rev         string       `json:"rev,omitempty"`
	Port        int          `json:"port,omitempty"`
	Actions     []actionSpec `json:"actions"`
}

func actionLabel(op string) string {
	switch op {
	case "start":
		return "Start"
	case "restart":
		return "Restart"
	case "stop":
		return "Stop"
	default:
		// Capitalize the first rune; op is a single lowercase ASCII verb, so
		// this avoids the deprecated (Unicode-unaware) strings.Title.
		if op == "" {
			return op
		}
		return strings.ToUpper(op[:1]) + op[1:]
	}
}

func blockedAction(op, reason string) actionSpec {
	return actionSpec{Op: op, Label: actionLabel(op), Risk: "blocked", Enabled: false, BlockedReason: reason}
}

func safeAction(op string) actionSpec {
	return actionSpec{Op: op, Label: actionLabel(op), Risk: "safe", Enabled: true}
}

func riskyAction(op string) actionSpec {
	return actionSpec{Op: op, Label: actionLabel(op), Risk: "risky", Enabled: true, Confirm: "double"}
}

func actionPolicy(kind, target, op string) actionSpec {
	if !okOps[op] {
		return blockedAction(op, "operation not allowed")
	}
	switch kind {
	case "service":
		if reCritical.MatchString(target) {
			return blockedAction(op, "critical system unit")
		}
		if target == "control-api.service" {
			return blockedAction(op, "control-api restart blocked until deploy/rollback jobs are fully decoupled")
		}
		if reAppUnit.MatchString(target) {
			if op == "stop" {
				return riskyAction(op)
			}
			return safeAction(op)
		}
		if infraUnits[target] {
			if target == "docker.service" && op == "restart" {
				return riskyAction(op)
			}
			return blockedAction(op, "infra unit only allows explicit approved operations")
		}
		return blockedAction(op, "service not in allowlist")
	case "container":
		if !reContName.MatchString(target) {
			return blockedAction(op, "invalid container name")
		}
		if op == "stop" {
			return riskyAction(op)
		}
		return safeAction(op)
	default:
		return blockedAction(op, "unknown target kind")
	}
}

func actionsForTarget(kind, target string) []actionSpec {
	ops := []string{"start", "restart", "stop"}
	if kind == "service" && infraUnits[target] {
		ops = []string{"restart"}
	}
	res := make([]actionSpec, 0, len(ops))
	for _, op := range ops {
		res = append(res, actionPolicy(kind, target, op))
	}
	return res
}
