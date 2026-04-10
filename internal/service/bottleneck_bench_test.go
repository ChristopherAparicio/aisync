//go:build bench_bottlenecks

// Package service — bottleneck micro-benchmark.
//
// This file is gated behind the `bench_bottlenecks` build tag so it is NEVER
// compiled as part of the regular test suite. It is a pure investigation tool
// used to measure the wall-clock cost of each "hot path" function called by
// the dashboard / costs / project-detail handlers.
//
// Usage:
//
//	AISYNC_BENCH_DB=~/.aisync/sessions.db \
//	  /opt/homebrew/bin/go test -tags=bench_bottlenecks \
//	  -run=TestBottleneckHotPaths -v -count=1 -timeout=10m \
//	  ./internal/service/...
//
// It opens the production DB READ-ONLY-ish (sqlite WAL mode allows concurrent
// readers while the daemon writes), constructs a plain SessionService via
// NewSessionService(), and calls each hot function twice (cold + warm) so we
// can see the SQLite page-cache effect.
//
// NB: this test does NOT mutate the DB. It only issues SELECTs.
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
)

func TestBottleneckHotPaths(t *testing.T) {
	dbPath := os.Getenv("AISYNC_BENCH_DB")
	if dbPath == "" {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".aisync", "sessions.db")
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("bench DB not found at %s: %v", dbPath, err)
	}

	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer func() { _ = store.Close() }()

	svc := NewSessionService(SessionServiceConfig{
		Store:   store,
		Pricing: pricing.NewCalculator(),
	})

	ctx := context.Background()
	now := time.Now()
	since7d := now.AddDate(0, 0, -7)
	since90d := now.AddDate(0, 0, -90)

	// Project under test (this repo itself — heaviest project in prod DB).
	const selfProject = "/Users/guardix/dev/aisync"

	type benchCase struct {
		name string
		fn   func() error
	}

	cases := []benchCase{
		{
			name: "Stats_AllProjects",
			fn: func() error {
				_, err := svc.Stats(StatsRequest{})
				return err
			},
		},
		{
			name: "Stats_SelfProject",
			fn: func() error {
				_, err := svc.Stats(StatsRequest{ProjectPath: selfProject})
				return err
			},
		},
		{
			name: "Trends_7d_AllProjects",
			fn: func() error {
				_, err := svc.Trends(ctx, TrendRequest{Period: 7 * 24 * time.Hour})
				return err
			},
		},
		{
			name: "Trends_7d_SelfProject",
			fn: func() error {
				_, err := svc.Trends(ctx, TrendRequest{ProjectPath: selfProject, Period: 7 * 24 * time.Hour})
				return err
			},
		},
		{
			name: "Forecast_weekly_90d_AllProjects",
			fn: func() error {
				_, err := svc.Forecast(ctx, ForecastRequest{Period: "weekly", Days: 90})
				return err
			},
		},
		{
			name: "Forecast_weekly_90d_SelfProject",
			fn: func() error {
				_, err := svc.Forecast(ctx, ForecastRequest{ProjectPath: selfProject, Period: "weekly", Days: 90})
				return err
			},
		},
		{
			name: "CacheEfficiency_7d_AllProjects",
			fn: func() error {
				_, err := svc.CacheEfficiency(ctx, "", since7d)
				return err
			},
		},
		{
			name: "CacheEfficiency_90d_AllProjects",
			fn: func() error {
				_, err := svc.CacheEfficiency(ctx, "", since90d)
				return err
			},
		},
		{
			name: "CacheEfficiency_90d_SelfProject",
			fn: func() error {
				_, err := svc.CacheEfficiency(ctx, selfProject, since90d)
				return err
			},
		},
		{
			name: "ContextSaturation_90d_AllProjects",
			fn: func() error {
				_, err := svc.ContextSaturation(ctx, "", since90d)
				return err
			},
		},
		{
			name: "ContextSaturation_90d_SelfProject",
			fn: func() error {
				_, err := svc.ContextSaturation(ctx, selfProject, since90d)
				return err
			},
		},
		{
			name: "AgentROI_90d_AllProjects",
			fn: func() error {
				_, err := svc.AgentROIAnalysis(ctx, "", since90d)
				return err
			},
		},
		{
			name: "AgentROI_90d_SelfProject",
			fn: func() error {
				_, err := svc.AgentROIAnalysis(ctx, selfProject, since90d)
				return err
			},
		},
		{
			name: "SkillROI_90d_AllProjects",
			fn: func() error {
				_, err := svc.SkillROIAnalysis(ctx, "", since90d)
				return err
			},
		},
		{
			name: "SkillROI_90d_SelfProject",
			fn: func() error {
				_, err := svc.SkillROIAnalysis(ctx, selfProject, since90d)
				return err
			},
		},
	}

	type result struct {
		name string
		cold time.Duration
		warm time.Duration
	}
	results := make([]result, 0, len(cases))

	for _, c := range cases {
		// Cold run.
		t0 := time.Now()
		if err := c.fn(); err != nil {
			t.Errorf("%s cold: %v", c.name, err)
			continue
		}
		cold := time.Since(t0)

		// Warm run (SQLite page cache + any in-proc caches).
		t1 := time.Now()
		if err := c.fn(); err != nil {
			t.Errorf("%s warm: %v", c.name, err)
			continue
		}
		warm := time.Since(t1)

		results = append(results, result{name: c.name, cold: cold, warm: warm})
	}

	// Pretty-print as a stable, greppable table.
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    BOTTLENECK HOT PATH BENCHMARK                         ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ %-44s %12s %12s ║\n", "function", "cold", "warm")
	fmt.Println("╠══════════════════════════════════════════════════════════════════════════╣")
	for _, r := range results {
		fmt.Printf("║ %-44s %12s %12s ║\n", r.name, r.cold.Round(time.Millisecond), r.warm.Round(time.Millisecond))
	}
	fmt.Println("╚══════════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}
