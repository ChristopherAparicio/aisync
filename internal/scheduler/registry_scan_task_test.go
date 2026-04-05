package scheduler

import (
	"context"
	"testing"
)

type mockRegistryScanner struct {
	scanCount int
	scanErr   error
}

func (m *mockRegistryScanner) ScanAllProjects() (int, error) {
	return m.scanCount, m.scanErr
}

func TestRegistryScanTask_Name(t *testing.T) {
	task := NewRegistryScanTask(RegistryScanTaskConfig{})
	if got := task.Name(); got != "registry_scan" {
		t.Errorf("Name() = %q, want %q", got, "registry_scan")
	}
}

func TestRegistryScanTask_NilScanner(t *testing.T) {
	task := NewRegistryScanTask(RegistryScanTaskConfig{})
	if err := task.Run(context.Background()); err != nil {
		t.Errorf("Run() with nil scanner should return nil, got %v", err)
	}
}

func TestRegistryScanTask_Run(t *testing.T) {
	scanner := &mockRegistryScanner{scanCount: 5}
	task := NewRegistryScanTask(RegistryScanTaskConfig{Scanner: scanner})

	if err := task.Run(context.Background()); err != nil {
		t.Errorf("Run() error = %v", err)
	}
}

func TestRegistryScanTask_ScanError(t *testing.T) {
	scanner := &mockRegistryScanner{
		scanCount: 0,
		scanErr:   context.DeadlineExceeded,
	}
	task := NewRegistryScanTask(RegistryScanTaskConfig{Scanner: scanner})

	err := task.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when scanner fails")
	}
}
