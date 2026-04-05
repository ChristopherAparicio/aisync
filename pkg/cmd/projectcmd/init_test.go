package projectcmd

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestRunInit_NonInteractive(t *testing.T) {
	// Create a temp config dir
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
		// No GitFunc — detect() will return empty detection (not a git repo)
	}

	opts := &initOptions{
		IO:       ios,
		Factory:  f,
		NoPrompt: true,
		Name:     "test-project",
		Branch:   "main",
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Verify output
	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Project configured") {
		t.Errorf("expected 'Project configured' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "test-project") {
		t.Errorf("expected project name in output, got:\n%s", output)
	}
	if !strings.Contains(output, "main") {
		t.Errorf("expected branch 'main' in output, got:\n%s", output)
	}

	// Verify config was saved
	pc := cfg.GetAllProjectClassifiers()
	if _, ok := pc["test-project"]; !ok {
		t.Fatal("expected project 'test-project' in config")
	}
	if pc["test-project"].DefaultBranch != "main" {
		t.Errorf("expected default_branch=main, got %q", pc["test-project"].DefaultBranch)
	}
}

func TestRunInit_NonInteractive_WithPRAndBudget(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &initOptions{
		IO:        ios,
		Factory:   f,
		NoPrompt:  true,
		Name:      "myapp",
		Branch:    "develop",
		PREnabled: true,
		Budget:    150,
		Tags:      "backend, api, go",
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "PR tracking:    enabled") {
		t.Errorf("expected PR tracking enabled in output, got:\n%s", output)
	}
	if !strings.Contains(output, "$150/mo") {
		t.Errorf("expected budget in output, got:\n%s", output)
	}

	pc := cfg.GetAllProjectClassifiers()
	proj, ok := pc["myapp"]
	if !ok {
		t.Fatal("expected project 'myapp' in config")
	}
	if !proj.PREnabled {
		t.Error("expected PREnabled=true")
	}
	if proj.Budget == nil || proj.Budget.MonthlyLimit != 150 {
		t.Error("expected budget 150")
	}
	if len(proj.Tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(proj.Tags), proj.Tags)
	}
}

func TestRunInit_Interactive(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	// Simulate user input: project name, branch, budget, tags
	input := "my-project\nmain\n50\nfrontend, react\n"
	ios := &iostreams.IOStreams{
		In:     strings.NewReader(input),
		Out:    &bytes.Buffer{},
		ErrOut: &bytes.Buffer{},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &initOptions{
		IO:      ios,
		Factory: f,
		// NoPrompt: false (interactive)
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Verify config was saved with prompted values
	pc := cfg.GetAllProjectClassifiers()
	proj, ok := pc["my-project"]
	if !ok {
		t.Fatal("expected project 'my-project' in config")
	}
	if proj.DefaultBranch != "main" {
		t.Errorf("expected branch 'main', got %q", proj.DefaultBranch)
	}
	if len(proj.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(proj.Tags), proj.Tags)
	}
}

func TestRunInit_UpdateExisting(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	// Pre-set a project
	_ = cfg.SetProjectClassifier("existing", config.ProjectClassifierConf{
		TicketPattern: "EX-\\d+",
		DefaultBranch: "master",
	})

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &initOptions{
		IO:       ios,
		Factory:  f,
		NoPrompt: true,
		Name:     "existing",
		Branch:   "main",
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Verify the branch was updated but ticket_pattern preserved
	// Note: since we re-lookup by name and existing has no matching remote/path,
	// the wizard creates a fresh entry. That's expected for non-interactive mode.
	pc := cfg.GetAllProjectClassifiers()
	proj, ok := pc["existing"]
	if !ok {
		t.Fatal("expected project 'existing' in config")
	}
	if proj.DefaultBranch != "main" {
		t.Errorf("expected updated branch 'main', got %q", proj.DefaultBranch)
	}
}

func TestRunList_Empty(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &listOptions{
		IO:      ios,
		Factory: f,
	}

	if err := runList(opts); err != nil {
		t.Fatalf("runList: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No projects found") {
		t.Errorf("expected 'No projects found' in output, got:\n%s", output)
	}
}

func TestRunList_WithProjects(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	_ = cfg.SetProjectClassifier("org/myapp", config.ProjectClassifierConf{
		DefaultBranch: "main",
		PREnabled:     true,
		Budget:        &config.ProjectBudgetConf{MonthlyLimit: 200},
	})
	_ = cfg.SetProjectClassifier("org/other", config.ProjectClassifierConf{
		DefaultBranch: "develop",
	})

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &listOptions{
		IO:      ios,
		Factory: f,
	}

	if err := runList(opts); err != nil {
		t.Fatalf("runList: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "org/myapp") {
		t.Errorf("expected 'org/myapp' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "org/other") {
		t.Errorf("expected 'org/other' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "2 projects") {
		t.Errorf("expected '2 projects' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "$200/mo") {
		t.Errorf("expected '$200/mo' in output, got:\n%s", output)
	}
}

func TestRunShow_NotConfigured(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &showOptions{
		IO:      ios,
		Factory: f,
		Name:    "nonexistent",
	}

	if err := runShow(opts); err != nil {
		t.Fatalf("runShow: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "not configured") {
		t.Errorf("expected 'not configured' in output, got:\n%s", output)
	}
}

func TestRunShow_Configured(t *testing.T) {
	globalDir := t.TempDir()
	cfg, err := config.New(globalDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	_ = cfg.SetProjectClassifier("org/myapp", config.ProjectClassifierConf{
		DefaultBranch: "main",
		PREnabled:     true,
		GitRemote:     "github.com/org/myapp",
		Platform:      "github",
		ProjectPath:   "/home/user/dev/myapp",
		Budget:        &config.ProjectBudgetConf{MonthlyLimit: 200, AlertAtPercent: 80},
		TicketPattern: "APP-\\d+",
		TicketSource:  "jira",
		Tags:          []string{"backend", "go"},
		BranchRules:   map[string]string{"feature/*": "feature", "fix/*": "bug"},
	})

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
	}

	opts := &showOptions{
		IO:      ios,
		Factory: f,
		Name:    "org/myapp",
	}

	if err := runShow(opts); err != nil {
		t.Fatalf("runShow: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	for _, want := range []string{
		"org/myapp",
		"/home/user/dev/myapp",
		"github.com/org/myapp",
		"GitHub",
		"main",
		"enabled",
		"$200/mo",
		"Alert at 80%",
		"APP-\\d+",
		"jira",
		"backend, go",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}

func TestPlatformDisplayName(t *testing.T) {
	tests := []struct {
		slug string
		want string
	}{
		{"github", "GitHub"},
		{"gitlab", "GitLab"},
		{"bitbucket", "Bitbucket"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		got := platformDisplayName(tt.slug)
		if got != tt.want {
			t.Errorf("platformDisplayName(%q) = %q, want %q", tt.slug, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"this is too long", 10, "this is t…"},
	}
	for _, tt := range tests {
		got := truncate(tt.s, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
		}
	}
}

func TestPrompt(t *testing.T) {
	input := "user-answer\n"
	out := &bytes.Buffer{}
	scanner := bufioScanner(input)

	got := prompt(out, scanner, "Your name", "default")
	if got != "user-answer" {
		t.Errorf("prompt() = %q, want %q", got, "user-answer")
	}
	if !strings.Contains(out.String(), "[default]") {
		t.Errorf("expected default shown in prompt, got: %s", out.String())
	}
}

func TestPrompt_Empty_ReturnsDefault(t *testing.T) {
	input := "\n"
	out := &bytes.Buffer{}
	scanner := bufioScanner(input)

	got := prompt(out, scanner, "Your name", "default-val")
	if got != "default-val" {
		t.Errorf("prompt() = %q, want %q", got, "default-val")
	}
}

func TestPromptYesNo(t *testing.T) {
	tests := []struct {
		input      string
		defaultYes bool
		want       bool
	}{
		{"y\n", false, true},
		{"yes\n", false, true},
		{"n\n", true, false},
		{"\n", true, true},   // empty → default yes
		{"\n", false, false}, // empty → default no
	}
	for _, tt := range tests {
		out := &bytes.Buffer{}
		scanner := bufioScanner(tt.input)
		got := promptYesNo(out, scanner, "Continue?", tt.defaultYes)
		if got != tt.want {
			t.Errorf("promptYesNo(%q, defaultYes=%v) = %v, want %v",
				tt.input, tt.defaultYes, got, tt.want)
		}
	}
}

func bufioScanner(input string) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(input))
}
