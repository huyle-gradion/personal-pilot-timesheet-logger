package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

func TestConfiguredDefaultCLIReachesReadyPlanWithoutDiscoveryOrWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("Go must be on PATH for CLI build: %v", err)
	}
	cacheCommand := exec.CommandContext(ctx, goExe, "env", "GOCACHE", "GOMODCACHE")
	cacheCommand.Env = []string{"HOME=" + os.Getenv("HOME"), "GOENV=off", "GOTOOLCHAIN=local", "GOCACHE=" + os.Getenv("GOCACHE"), "GOMODCACHE=" + os.Getenv("GOMODCACHE"), "GOPATH=" + os.Getenv("GOPATH")}
	cacheOutput, err := cacheCommand.Output()
	if err != nil {
		t.Fatalf("resolve Go build caches: %v", err)
	}
	caches := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
	if len(caches) != 2 {
		t.Fatalf("unexpected Go cache paths: %q", cacheOutput)
	}
	cfg, args := configuredDefaultFixture(t)
	stub := &stubRemote{entries: map[string][]map[string]any{}}
	server := httptest.NewServer(stub)
	defer server.Close()
	updateDefaultConfig(t, cfg, func(c *timesheet.Config) { c.Gradion.Endpoint = server.URL })
	meeting := timedBlock("meeting", "14:00", "15:00", "Discuss synthetic design")
	meeting.Kind = "meeting"
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("work", "08:00", "13:00", "Implement synthetic design"), meeting})
	binary := filepath.Join(t.TempDir(), "timesheet")
	build := exec.CommandContext(ctx, goExe, "build", "-o", binary, ".")
	build.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + filepath.Dir(goExe) + ":/usr/bin:/bin", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOENV=off", "GOCACHE=" + caches[0], "GOMODCACHE=" + caches[1]}
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build isolated CLI: %v\n%s", err, out)
	}
	cliEnv := []string{"HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR"), "SYNTHETIC_TOKEN=synthetic"}
	run := func(argv []string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, binary, argv...)
		command.Env = cliEnv
		out, err := command.Output()
		if err != nil {
			t.Fatalf("CLI %v: %v", argv, err)
		}
		return out
	}
	for _, expected := range []string{"stage", "classify", "save", "plan"} {
		var step nextAction
		if err := json.Unmarshal(run(append([]string{"next"}, args...)), &step); err != nil {
			t.Fatal(err)
		}
		if step.State != stateAction || step.Step != expected || step.Action == nil {
			t.Fatalf("want %s, got %+v", expected, step)
		}
		if expected == "classify" && (!slices.Contains(step.Action.Cmd, "--configured-default") || slices.Contains(step.Action.Cmd, "--file")) {
			t.Fatalf("default must need no answer file: %+v", step)
		}
		run(step.Action.Cmd[1:])
	}
	var ready nextAction
	if err := json.Unmarshal(run(append([]string{"next"}, args...)), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.State != stateHumanGates {
		t.Fatalf("ready = %+v", ready)
	}
	store, err := timesheet.OpenStore(statePathFor(cfg))
	if err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if len(state.Days[testDate].Confirmed) != 3 {
		t.Fatalf("confirmed = %+v", state.Days[testDate].Confirmed)
	}
	for _, iv := range state.Days[testDate].Confirmed {
		if iv.RemoteClassificationID != "cls-academy" {
			t.Fatalf("default not selected: %+v", iv)
		}
	}
	if len(state.Approvals)+len(state.Intents)+len(state.Executions) != 0 {
		t.Fatal("automatic flow created human-gated records")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.calls) == 0 {
		t.Fatal("ready preview must observe remote week")
	}
	for _, call := range stub.calls {
		if call != "timesheet__get_my_week" {
			t.Fatalf("unexpected discovery or write call: %s", call)
		}
	}
	updateDefaultConfig(t, cfg, func(c *timesheet.Config) { c.Gradion.DefaultClassification.Name = "Changed future default" })
	var unchanged nextAction
	if err := json.Unmarshal(run(append([]string{"next"}, args...)), &unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged.State != stateHumanGates {
		t.Fatalf("changed default must not rewrite saved selections: %+v", unchanged)
	}
	command := exec.CommandContext(ctx, binary, append([]string{"classify"}, append(args, "--configured-default")...)...)
	command.Env = cliEnv
	if out, err := command.CombinedOutput(); err == nil {
		t.Fatalf("saved default command accepted: %s", out)
	}
}
