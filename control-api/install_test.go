package main

import (
	"strings"
	"testing"
)

func TestValidateInstallRunners(t *testing.T) {
	base := installRequest{Name: "x", Catalog: "c", Module: "m", Version: "1", Repo: "https://github.com/a/b", SHA: strings.Repeat("a", 40)}

	img := base
	img.Runner = "image"
	img.Image = "a/b"
	if err := validateInstall(img); err != nil {
		t.Fatalf("image valid: %v", err)
	}

	proc := base
	proc.Runner = "process"
	if validateInstall(proc) == nil {
		t.Fatal("process without runtime/cmds should fail")
	}
	proc.Runtime = "nodejs_22"
	proc.BuildCmd = "npm ci"
	proc.StartCmd = "npm start"
	if err := validateInstall(proc); err != nil {
		t.Fatalf("process valid: %v", err)
	}

	comp := base
	comp.Runner = "compose"
	if validateInstall(comp) == nil {
		t.Fatal("compose without dir should fail")
	}
	comp.Dir = "./svc"
	if err := validateInstall(comp); err != nil {
		t.Fatalf("compose valid: %v", err)
	}

	bad := base
	bad.Runner = "wat"
	if validateInstall(bad) == nil {
		t.Fatal("unknown runner should fail")
	}
}

func TestGenerateV2ModuleProcess(t *testing.T) {
	req := installRequest{
		Name: "api", Runner: "process", Repo: "https://github.com/a/b",
		SHA: strings.Repeat("c", 40), Runtime: "nodejs_22",
		BuildCmd: "npm ci", StartCmd: "npm start", Port: 3000,
	}
	out := generateV2Module(req)
	for _, want := range []string{`runner = "process";`, `runtime = "nodejs_22";`, "buildCmd =", "startCmd =", `rev = "` + strings.Repeat("c", 40)} {
		if !strings.Contains(out, want) {
			t.Fatalf("process module missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `image =`) {
		t.Fatal("process module must not emit an image field")
	}
}

func TestRetargetVolumeClass(t *testing.T) {
	src := "{\n  volumes = [\n    { name = \"config\"; kind = \"config\"; class = \"local\"; }\n  ];\n}\n"
	next, old, ok := retargetVolumeClass(src, "config", "nas")
	if !ok || old != "local" {
		t.Fatalf("retarget failed: ok=%v old=%q", ok, old)
	}
	if !strings.Contains(next, `class = "nas"`) || strings.Contains(next, `class = "local"`) {
		t.Fatalf("class not retargeted:\n%s", next)
	}
	if _, _, ok := retargetVolumeClass(src, "missing", "nas"); ok {
		t.Fatal("missing volume should not match")
	}
}
