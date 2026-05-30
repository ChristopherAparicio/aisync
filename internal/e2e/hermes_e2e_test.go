package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/hermes"
	hermestestdata "github.com/ChristopherAparicio/aisync/internal/provider/hermes/testdata"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/internal/web"
)

func TestHermesE2E(t *testing.T) {
	dbPath := hermestestdata.NewFixtureDB(t)
	hermesHome := filepath.Dir(dbPath)

	cronDir := filepath.Join(hermesHome, "cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatalf("mkdir cron: %v", err)
	}
	createdAt := time.Now().UTC()
	cronFile := session.CronJobsFile{
		Jobs: []session.CronJob{
			{
				ID:              "job123",
				Name:            "fixture-cron-job",
				Prompt:          "Run nightly tests",
				Schedule:        "0 0 * * *",
				ScheduleDisplay: "Daily at midnight",
				Repeat:          true,
				Enabled:         true,
				Provider:        "hermes",
				CreatedAt:       &createdAt,
			},
		},
	}
	cronData, err := json.Marshal(cronFile)
	if err != nil {
		t.Fatalf("marshal cronFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cronDir, "jobs.json"), cronData, 0o644); err != nil {
		t.Fatalf("write jobs.json: %v", err)
	}

	store := testutil.MustOpenStore(t)

	prov := hermes.New(hermesHome)
	reg := provider.NewRegistry(prov)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	summaries, err := prov.Detect("", "")
	if err != nil {
		t.Fatalf("hermes Detect: %v", err)
	}
	if len(summaries) == 0 {
		t.Fatal("hermes Detect returned no sessions — fixture not loaded")
	}

	for _, sum := range summaries {
		if _, capErr := svc.CaptureByID(service.CaptureRequest{
			ProviderName: session.ProviderHermes,
			Mode:         session.StorageModeFull,
		}, sum.ID); capErr != nil {
			t.Fatalf("CaptureByID(%s): %v", sum.ID, capErr)
		}
	}

	jobs, err := hermes.ParseCronJobs(hermesHome)
	if err != nil {
		t.Fatalf("ParseCronJobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("ParseCronJobs returned no jobs")
	}
	for i := range jobs {
		if upErr := store.UpsertCronJob(&jobs[i]); upErr != nil {
			t.Fatalf("UpsertCronJob: %v", upErr)
		}
	}

	webSrv, err := web.New(web.Config{
		SessionService: svc,
		Store:          store,
		Addr:           ":0",
	})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	handler := webSrv.Handler()

	get := func(path string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	t.Run("sessions_lists_hermes_session", func(t *testing.T) {
		code, body := get("/sessions")
		if code != http.StatusOK {
			t.Fatalf("GET /sessions: status %d, want 200", code)
		}
		if !strings.Contains(body, "fixture-parent-001") {
			t.Errorf("GET /sessions: expected 'fixture-parent-001' in body (len %d)", len(body))
		}
	})

	t.Run("exchanges_responds_200_with_content", func(t *testing.T) {
		code, body := get("/sessions/fixture-parent-001/exchanges")
		if code != http.StatusOK {
			t.Fatalf("GET /sessions/fixture-parent-001/exchanges: status %d, want 200\n%.500s", code, body)
		}
		hasContent := strings.Contains(body, "fixture-parent-001") ||
			strings.Contains(body, "delegate_task") ||
			strings.Contains(body, "hello from sentinel")
		if !hasContent {
			t.Errorf("exchanges page missing expected content; snippet: %.500s", body)
		}
	})

	t.Run("cron_responds_200_with_job", func(t *testing.T) {
		code, body := get("/cron")
		if code != http.StatusOK {
			t.Fatalf("GET /cron: status %d, want 200\n%.500s", code, body)
		}
		if !strings.Contains(body, "fixture-cron-job") {
			t.Errorf("GET /cron: expected 'fixture-cron-job' in body")
		}
	})

	t.Run("graph_responds_200", func(t *testing.T) {
		code, body := get("/graph")
		if code != http.StatusOK {
			t.Fatalf("GET /graph: status %d, want 200\n%.500s", code, body)
		}
		_ = body
	})
}
