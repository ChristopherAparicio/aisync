package hermes

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ParseCronJobs reads $hermesHome/cron/jobs.json and returns the list of
// scheduled Hermes jobs as domain CronJob values.
//
// Tolerances:
//   - Missing file returns (nil, nil) — not an error.
//   - Malformed or truncated JSON logs a warning and returns whatever was
//     successfully decoded before the error (may be empty).
func ParseCronJobs(hermesHome string) ([]session.CronJob, error) {
	cronPath := filepath.Join(hermesHome, "cron", "jobs.json")

	data, err := os.ReadFile(cronPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var envelope session.CronJobsFile
	if err := json.Unmarshal(data, &envelope); err != nil {
		log.Printf("hermes: cron: malformed jobs.json (%s): %v - returning partial results", cronPath, err)
		return partialDecodeCronJobs(data), nil
	}

	return envelope.Jobs, nil
}

// partialDecodeCronJobs extracts as many valid CronJob entries as possible from
// data whose top-level JSON may be truncated or invalid. It walks the envelope
// token-by-token and decodes jobs one at a time, stopping at the first decode
// error rather than panicking.
func partialDecodeCronJobs(data []byte) []session.CronJob {
	dec := json.NewDecoder(bytes.NewReader(data))

	// Consume opening '{'.
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			break
		}
		key, _ := keyTok.(string)

		if key != "jobs" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				break
			}
			continue
		}

		if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
			break
		}
		var jobs []session.CronJob
		for dec.More() {
			var job session.CronJob
			if err := dec.Decode(&job); err != nil {
				break
			}
			jobs = append(jobs, job)
		}
		return jobs
	}
	return nil
}
