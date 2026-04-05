package scheduler

import (
	"context"
	"log"
)

// RegistryScanner is the port interface for the registry service.
// It avoids importing the full service package (dependency inversion).
type RegistryScanner interface {
	// ScanAllProjects discovers and scans all known projects,
	// persisting snapshots and flat capabilities. Returns the
	// number of projects successfully scanned.
	ScanAllProjects() (int, error)
}

// RegistryScanTask periodically scans all projects for capability changes.
// It persists both JSON snapshots (audit trail) and flat capability records
// (queryable index) via the RegistryService.
type RegistryScanTask struct {
	scanner RegistryScanner
	logger  *log.Logger
}

// RegistryScanTaskConfig configures the registry scan task.
type RegistryScanTaskConfig struct {
	Scanner RegistryScanner
	Logger  *log.Logger
}

// NewRegistryScanTask creates a new registry scan task.
func NewRegistryScanTask(cfg RegistryScanTaskConfig) *RegistryScanTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &RegistryScanTask{
		scanner: cfg.Scanner,
		logger:  logger,
	}
}

// Name returns the task identifier.
func (t *RegistryScanTask) Name() string {
	return "registry_scan"
}

// Run scans all discovered projects for capability changes.
func (t *RegistryScanTask) Run(_ context.Context) error {
	if t.scanner == nil {
		return nil
	}

	count, err := t.scanner.ScanAllProjects()
	if err != nil {
		t.logger.Printf("[registry_scan] scan failed: %v", err)
		return err
	}

	t.logger.Printf("[registry_scan] scanned %d projects", count)
	return nil
}
