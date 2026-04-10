package diagnostic

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Registry tests ──────────────────────────────────────────────────────────

func TestDefaultModules_containsBuiltins(t *testing.T) {
	modules := DefaultModules()

	names := make(map[string]bool)
	for _, m := range modules {
		names[m.Name()] = true
	}

	for _, expected := range []string{"core", "rtk", "images", "api"} {
		if !names[expected] {
			t.Errorf("expected built-in module %q in DefaultModules()", expected)
		}
	}
}

func TestDefaultModules_returnsCopy(t *testing.T) {
	a := DefaultModules()
	b := DefaultModules()

	if len(a) == 0 {
		t.Fatal("DefaultModules() returned empty slice")
	}

	// Mutate a — b should be unaffected.
	a[0] = nil
	if b[0] == nil {
		t.Error("DefaultModules() should return independent copies")
	}
}

func TestRegisterModule_customModule(t *testing.T) {
	// Save and restore registry state.
	saved := DefaultModules()
	defer func() {
		resetRegistry()
		for _, m := range saved {
			RegisterModule(m)
		}
	}()

	resetRegistry()
	// Re-register builtins so we start from a known state.
	RegisterModule(&CoreModule{})

	// Register a custom module.
	custom := &stubModule{name: "custom-test"}
	RegisterModule(custom)

	modules := DefaultModules()
	names := registeredModuleNames()

	if len(modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(modules))
	}
	found := false
	for _, n := range names {
		if n == "custom-test" {
			found = true
		}
	}
	if !found {
		t.Error("custom module not found after RegisterModule()")
	}
}

func TestRegisterModule_duplicatePanics(t *testing.T) {
	// Save and restore registry state.
	saved := DefaultModules()
	defer func() {
		resetRegistry()
		for _, m := range saved {
			RegisterModule(m)
		}
	}()

	resetRegistry()
	RegisterModule(&stubModule{name: "dup"})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate module name")
		}
		msg, ok := r.(string)
		if !ok || msg == "" {
			t.Errorf("expected string panic message, got %v", r)
		}
	}()

	RegisterModule(&stubModule{name: "dup"}) // must panic
}

func TestResetRegistry_clearsAll(t *testing.T) {
	// Save and restore registry state.
	saved := DefaultModules()
	defer func() {
		resetRegistry()
		for _, m := range saved {
			RegisterModule(m)
		}
	}()

	resetRegistry()
	modules := DefaultModules()
	if len(modules) != 0 {
		t.Errorf("expected empty registry after reset, got %d modules", len(modules))
	}

	// Can re-register after reset.
	RegisterModule(&stubModule{name: "after-reset"})
	modules = DefaultModules()
	if len(modules) != 1 {
		t.Errorf("expected 1 module after re-register, got %d", len(modules))
	}
}

func TestRegisteredModuleNames(t *testing.T) {
	names := registeredModuleNames()
	if len(names) < 4 {
		t.Errorf("expected at least 4 registered module names, got %d", len(names))
	}
}

// stubModule is a minimal AnalysisModule for testing the registry.
type stubModule struct {
	name     string
	activate bool
}

func (m *stubModule) Name() string                                          { return m.name }
func (m *stubModule) ShouldActivate(_ *session.Session) bool                { return m.activate }
func (m *stubModule) Detect(_ *InspectReport, _ *session.Session) []Problem { return nil }

// ── Module activation tests ─────────────────────────────────────────────────

func TestCoreModule_alwaysActive(t *testing.T) {
	mod := &CoreModule{}
	if mod.Name() != "core" {
		t.Errorf("expected name 'core', got %q", mod.Name())
	}
	// Empty session
	sess := &session.Session{}
	if !mod.ShouldActivate(sess) {
		t.Error("core module must always activate")
	}
}

func TestImagesModule_activatesOnInlineImages(t *testing.T) {
	mod := &ImagesModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleUser,
				Images: []session.ImageMeta{
					{MediaType: "image/png", SizeBytes: 1000},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("images module should activate when session has inline images")
	}
}

func TestImagesModule_activatesOnContentBlockImages(t *testing.T) {
	mod := &ImagesModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ContentBlocks: []session.ContentBlock{
					{
						Type:  session.ContentBlockImage,
						Image: &session.ImageMeta{MediaType: "image/jpeg"},
					},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("images module should activate on ContentBlock images")
	}
}

func TestImagesModule_activatesOnSimctlCommand(t *testing.T) {
	mod := &ImagesModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "xcrun simctl io booted screenshot /tmp/ss.png"}`},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("images module should activate on simctl screenshot commands")
	}
}

func TestImagesModule_activatesOnImageRead(t *testing.T) {
	mod := &ImagesModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_read", Input: `{"filePath": "/tmp/screenshot.png"}`},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("images module should activate on image file reads")
	}
}

func TestImagesModule_inactiveForBackendSession(t *testing.T) {
	mod := &ImagesModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "go test ./..."}`},
					{Name: "mcp_read", Input: `{"filePath": "/src/main.go"}`},
				},
			},
		},
	}
	if mod.ShouldActivate(sess) {
		t.Error("images module should NOT activate for backend-only sessions")
	}
}

func TestRTKModule_activatesOnRTKCommand(t *testing.T) {
	mod := &RTKModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "rtk go test ./..."}`},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("rtk module should activate when session uses rtk")
	}
}

func TestRTKModule_activatesOnRTKFullPath(t *testing.T) {
	mod := &RTKModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_bash", Input: `{"command": "/opt/homebrew/bin/rtk curl http://localhost:8080"}`},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("rtk module should activate on full-path rtk invocation")
	}
}

func TestRTKModule_inactiveWithoutRTK(t *testing.T) {
	mod := &RTKModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "go test ./..."}`},
					{Name: "bash", Input: `{"command": "curl http://localhost:8080"}`},
				},
			},
		},
	}
	if mod.ShouldActivate(sess) {
		t.Error("rtk module should NOT activate without rtk commands")
	}
}

func TestAPIModule_activatesOnCurl(t *testing.T) {
	mod := &APIModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "curl -X POST http://localhost:8080/api/login"}`},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("api module should activate when session uses curl")
	}
}

func TestAPIModule_activatesOnWget(t *testing.T) {
	mod := &APIModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "wget http://example.com/file.tar.gz"}`},
				},
			},
		},
	}
	if !mod.ShouldActivate(sess) {
		t.Error("api module should activate on wget commands")
	}
}

func TestAPIModule_inactiveWithoutHTTP(t *testing.T) {
	mod := &APIModule{}
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "go build ./cmd/server"}`},
				},
			},
		},
	}
	if mod.ShouldActivate(sess) {
		t.Error("api module should NOT activate without HTTP client commands")
	}
}

// ── RunModules integration tests ────────────────────────────────────────────

func TestRunModules_coreAlwaysRuns(t *testing.T) {
	sess := &session.Session{}
	r := makeReport()
	modules := DefaultModules()

	_, results := RunModules(modules, sess, r)

	// Core must be activated
	found := false
	for _, mr := range results {
		if mr.Name == "core" {
			found = true
			if !mr.Activated {
				t.Error("core module should always be activated")
			}
		}
	}
	if !found {
		t.Error("core module not found in results")
	}
}

func TestRunModules_onlyCoreForBackendSession(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "go test ./..."}`},
			}},
		},
	}
	r := makeReport()
	modules := DefaultModules()

	_, results := RunModules(modules, sess, r)

	for _, mr := range results {
		switch mr.Name {
		case "core":
			if !mr.Activated {
				t.Error("core should be activated")
			}
		case "rtk", "images", "api":
			if mr.Activated {
				t.Errorf("module %q should NOT be activated for backend-only session", mr.Name)
			}
		}
	}
}

func TestRunModules_multipleModulesActivate(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl http://localhost:8080/api/login"}`},
				{Name: "mcp_read", Input: `{"filePath": "/tmp/screenshot.png"}`},
			}},
		},
	}
	r := makeReport()
	modules := DefaultModules()

	_, results := RunModules(modules, sess, r)

	activated := make(map[string]bool)
	for _, mr := range results {
		if mr.Activated {
			activated[mr.Name] = true
		}
	}

	// core, rtk, images, api should all activate
	for _, name := range []string{"core", "rtk", "images", "api"} {
		if !activated[name] {
			t.Errorf("expected module %q to be activated", name)
		}
	}
}

func TestRunModules_moduleResultsRecorded(t *testing.T) {
	sess := &session.Session{}
	r := makeReport()
	modules := DefaultModules()

	_, results := RunModules(modules, sess, r)

	if len(results) != 4 {
		t.Errorf("expected 4 module results, got %d", len(results))
	}

	// Check that all expected modules are present
	names := make(map[string]bool)
	for _, mr := range results {
		names[mr.Name] = true
	}
	for _, expected := range []string{"core", "rtk", "images", "api"} {
		if !names[expected] {
			t.Errorf("expected module %q in results", expected)
		}
	}
}

// ── RTK detector tests ──────────────────────────────────────────────────────

func TestDetectRTKCurlConflict_triggers(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		CurlViaRTK: 15,
		CurlDirect: 5,
	})
	problems := detectRTKCurlConflict(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemRTKCurlConflict {
		t.Errorf("wrong ID: %s", p.ID)
	}
	if p.Severity != SeverityHigh {
		t.Errorf("expected high severity for 15 curl-via-rtk, got %s", p.Severity)
	}
}

func TestDetectRTKCurlConflict_belowThreshold(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		CurlViaRTK: 2, // below threshold of 3
		CurlDirect: 10,
	})
	problems := detectRTKCurlConflict(r)
	if len(problems) != 0 {
		t.Errorf("expected no problems for 2 curl-via-rtk, got %d", len(problems))
	}
}

func TestDetectRTKCurlConflict_nilStats(t *testing.T) {
	r := makeReport()
	problems := detectRTKCurlConflict(r)
	if len(problems) != 0 {
		t.Error("expected no problems with nil rtkStats")
	}
}

func TestDetectRTKSecretRedaction_triggers(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		RedactedOutputs: 8,
	})
	problems := detectRTKSecretRedaction(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemRTKSecretRedaction {
		t.Errorf("wrong ID: %s", p.ID)
	}
	if p.Severity != SeverityHigh {
		t.Errorf("expected high severity for 8 redacted outputs, got %s", p.Severity)
	}
}

func TestDetectRTKSecretRedaction_belowThreshold(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		RedactedOutputs: 1,
	})
	problems := detectRTKSecretRedaction(r)
	if len(problems) != 0 {
		t.Error("expected no problems for 1 redacted output")
	}
}

func TestDetectRTKIdenticalRetry_triggers(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		RetryBursts: []RetryBurst{
			{Command: "rtk curl http://localhost/api/login", Count: 5, StartMsgIdx: 100, EndMsgIdx: 104},
			{Command: "rtk go test ./...", Count: 4, StartMsgIdx: 200, EndMsgIdx: 203},
		},
	})
	problems := detectRTKIdenticalRetry(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemRTKIdenticalRetry {
		t.Errorf("wrong ID: %s", p.ID)
	}
	// 2 bursts, max burst = 5. Threshold for high: >3 bursts OR maxBurst > 5.
	// Neither met (2 ≤ 3, 5 ≤ 5), so medium.
	if p.Severity != SeverityMedium {
		t.Errorf("expected medium severity for 2 bursts (max 5), got %s", p.Severity)
	}
	// Total wasted should be 9 (5 + 4)
	if p.Metric != 9 {
		t.Errorf("expected metric 9, got %.0f", p.Metric)
	}
}

func TestDetectRTKIdenticalRetry_highSeverity(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		RetryBursts: []RetryBurst{
			{Command: "rtk curl http://localhost/api/login", Count: 8, StartMsgIdx: 100, EndMsgIdx: 107},
		},
	})
	problems := detectRTKIdenticalRetry(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high severity for burst of 8 (>5), got %s", problems[0].Severity)
	}
}

func TestDetectRTKIdenticalRetry_noRetries(t *testing.T) {
	r := makeReport()
	r.SetModuleData("rtk", &RTKAnalysis{
		RetryBursts: nil,
	})
	problems := detectRTKIdenticalRetry(r)
	if len(problems) != 0 {
		t.Error("expected no problems with no retry bursts")
	}
}

// ── API detector tests ──────────────────────────────────────────────────────

func TestDetectAPIRetryLoop_triggers(t *testing.T) {
	r := makeReport()
	r.SetModuleData("api", &APIAnalysis{
		EndpointCalls: []EndpointCall{
			{URL: "http://localhost:8080/api/auth/login", Count: 23},
			{URL: "http://localhost:8080/api/users", Count: 3},
		},
	})
	problems := detectAPIRetryLoop(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemAPIRetryLoop {
		t.Errorf("wrong ID: %s", p.ID)
	}
	if p.Severity != SeverityHigh {
		t.Errorf("expected high severity for 23 calls, got %s", p.Severity)
	}
}

func TestDetectAPIRetryLoop_belowThreshold(t *testing.T) {
	r := makeReport()
	r.SetModuleData("api", &APIAnalysis{
		EndpointCalls: []EndpointCall{
			{URL: "http://localhost:8080/api/auth/login", Count: 4},
		},
	})
	problems := detectAPIRetryLoop(r)
	if len(problems) != 0 {
		t.Error("expected no problems for 4 calls to same endpoint")
	}
}

func TestDetectAPIRetryLoop_nilStats(t *testing.T) {
	r := makeReport()
	problems := detectAPIRetryLoop(r)
	if len(problems) != 0 {
		t.Error("expected no problems with nil apiStats")
	}
}

func TestDetectIdenticalCommandBurst_triggers(t *testing.T) {
	r := makeReport()
	r.SetModuleData("api", &APIAnalysis{
		CommandBursts: []CommandBurst{
			{Command: "curl -X POST http://localhost:8080/api/login", Count: 5, StartMsgIdx: 100, EndMsgIdx: 104},
		},
	})
	problems := detectIdenticalCommandBurst(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemIdenticalCommandBurst {
		t.Errorf("wrong ID: %s", p.ID)
	}
}

func TestDetectIdenticalCommandBurst_noBursts(t *testing.T) {
	r := makeReport()
	r.SetModuleData("api", &APIAnalysis{
		CommandBursts: nil,
	})
	problems := detectIdenticalCommandBurst(r)
	if len(problems) != 0 {
		t.Error("expected no problems with no command bursts")
	}
}

// ── RTK stats builder tests ─────────────────────────────────────────────────

func TestBuildRTKStats_curlViaRTK(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl -X POST http://localhost:8080/api/login"}`, Output: `{code: string, detail: string}`},
			}},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "curl -X POST http://localhost:8080/api/login"}`, Output: `{"access_token":"eyJ..."}`},
			}},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl http://localhost:8080/api/users"}`, Output: `[{id: number, name: string}]`},
			}},
		},
	}
	stats := buildRTKStats(sess)
	if stats.TotalRTKCmds != 2 {
		t.Errorf("expected 2 RTK commands, got %d", stats.TotalRTKCmds)
	}
	if stats.CurlViaRTK != 2 {
		t.Errorf("expected 2 curl-via-RTK, got %d", stats.CurlViaRTK)
	}
	if stats.CurlDirect != 1 {
		t.Errorf("expected 1 direct curl, got %d", stats.CurlDirect)
	}
}

func TestBuildRTKStats_redactionDetection(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl http://localhost/login"}`,
					Output: `{"token": "***REDACTED:GENERIC_SECRET***"}`},
			}},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl http://localhost/login"}`,
					Output: `{"token": "***REDACTED***"}`},
			}},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk go test ./..."}`,
					Output: `PASS ok test 0.5s`},
			}},
		},
	}
	stats := buildRTKStats(sess)
	if stats.RedactedOutputs != 2 {
		t.Errorf("expected 2 redacted outputs, got %d", stats.RedactedOutputs)
	}
}

func TestBuildRTKStats_RetryBurstDetection(t *testing.T) {
	// Simulate 5 identical RTK commands
	msgs := make([]session.Message, 5)
	for i := range msgs {
		msgs[i] = session.Message{
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl -X POST http://localhost:8080/api/login"}`},
			},
		}
	}
	sess := &session.Session{Messages: msgs}
	stats := buildRTKStats(sess)
	if len(stats.RetryBursts) != 1 {
		t.Fatalf("expected 1 retry burst, got %d", len(stats.RetryBursts))
	}
	if stats.RetryBursts[0].Count != 5 {
		t.Errorf("expected burst of 5, got %d", stats.RetryBursts[0].Count)
	}
}

func TestBuildRTKStats_noBurstUnder3(t *testing.T) {
	msgs := make([]session.Message, 2)
	for i := range msgs {
		msgs[i] = session.Message{
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "rtk curl http://localhost/api"}`},
			},
		}
	}
	sess := &session.Session{Messages: msgs}
	stats := buildRTKStats(sess)
	if len(stats.RetryBursts) != 0 {
		t.Errorf("expected no bursts for 2 identical commands, got %d", len(stats.RetryBursts))
	}
}

// ── API stats builder tests ─────────────────────────────────────────────────

func TestBuildAPIStats_endpointCounting(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "curl -X POST http://localhost:8080/api/auth/login"}`},
			}},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "curl -X POST http://localhost:8080/api/auth/login"}`},
			}},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "curl http://localhost:8080/api/users"}`},
			}},
		},
	}
	stats := buildAPIStats(sess)
	loginCount := 0
	for _, ep := range stats.EndpointCalls {
		if ep.URL == "http://localhost:8080/api/auth/login" {
			loginCount = ep.Count
		}
	}
	if loginCount != 2 {
		t.Errorf("expected /api/auth/login called 2 times, got %d", loginCount)
	}
}

func TestBuildAPIStats_CommandBurstDetection(t *testing.T) {
	msgs := make([]session.Message, 4)
	for i := range msgs {
		msgs[i] = session.Message{
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "curl -X POST http://localhost:8080/api/login"}`},
			},
		}
	}
	sess := &session.Session{Messages: msgs}
	stats := buildAPIStats(sess)
	if len(stats.CommandBursts) != 1 {
		t.Fatalf("expected 1 command burst, got %d", len(stats.CommandBursts))
	}
	if stats.CommandBursts[0].Count != 4 {
		t.Errorf("expected burst of 4, got %d", stats.CommandBursts[0].Count)
	}
}

func TestBuildAPIStats_noHTTPCommands(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "bash", Input: `{"command": "go test ./..."}`},
			}},
		},
	}
	stats := buildAPIStats(sess)
	if len(stats.EndpointCalls) != 0 {
		t.Errorf("expected no endpoint calls for non-HTTP session, got %d", len(stats.EndpointCalls))
	}
}

func TestExtractCurlURL_basic(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`curl http://localhost:8080/api/login`, "http://localhost:8080/api/login"},
		{`curl -X POST https://example.com/api/users`, "https://example.com/api/users"},
		{`curl -H "Authorization: Bearer token" http://localhost/api`, "http://localhost/api"},
		{`curl http://localhost/api?foo=bar&baz=1`, "http://localhost/api"},
		{`echo hello`, ""},
	}
	for _, tt := range tests {
		got := ExtractCurlURL(tt.input)
		if got != tt.expected {
			t.Errorf("ExtractCurlURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// ── BuildReport integration with modules ────────────────────────────────────

func TestBuildReport_populatesModuleResults(t *testing.T) {
	sess := &session.Session{
		ID:       "ses_test_modules",
		Provider: session.ProviderOpenCode,
		Agent:    "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "test"},
			{Role: session.RoleAssistant, Content: "done"},
		},
	}
	r := BuildReport(sess, nil)
	if len(r.ModuleResults) == 0 {
		t.Fatal("expected ModuleResults to be populated")
	}
	// core should be activated
	for _, mr := range r.ModuleResults {
		if mr.Name == "core" && !mr.Activated {
			t.Error("core module should be activated in BuildReport")
		}
	}
}

func TestBuildReport_rtkModuleActivatesForRTKSession(t *testing.T) {
	sess := &session.Session{
		ID:       "ses_rtk_test",
		Provider: session.ProviderOpenCode,
		Agent:    "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "run tests"},
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command": "rtk go test ./..."}`, State: session.ToolStateCompleted},
				},
			},
		},
	}
	r := BuildReport(sess, nil)
	found := false
	for _, mr := range r.ModuleResults {
		if mr.Name == "rtk" {
			found = true
			if !mr.Activated {
				t.Error("rtk module should be activated for session with rtk commands")
			}
		}
	}
	if !found {
		t.Error("rtk module not found in ModuleResults")
	}
}
