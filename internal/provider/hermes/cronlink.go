package hermes

import (
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// CronRunLink associates a session whose ID follows the cron_{job_id}_{timestamp}
// naming convention with its originating CronJob.
type CronRunLink struct {
	SessionID session.ID
	JobID     string
	Job       *session.CronJob // nil when Orphan is true
	Orphan    bool             // true when no matching job was found
}

// LinkCronRuns correlates sessions whose IDs follow the cron_{job_id}_{timestamp}
// naming convention to their corresponding CronJob entries. Sessions whose IDs do
// not start with "cron_" are skipped entirely. Sessions with a cron-prefixed ID
// that has no matching job are returned with Orphan=true and Job=nil.
func LinkCronRuns(jobs []session.CronJob, sessions []session.Summary) []CronRunLink {
	jobMap := make(map[string]*session.CronJob, len(jobs))
	for i := range jobs {
		jobMap[jobs[i].ID] = &jobs[i]
	}

	var links []CronRunLink
	for _, s := range sessions {
		id := string(s.ID)
		if !strings.HasPrefix(id, "cron_") {
			continue
		}

		parts := strings.Split(id, "_")
		// Need at least 3 segments: "cron", <job_id>, <timestamp>.
		if len(parts) < 3 {
			continue
		}

		// Job ID is everything between the leading "cron" and the trailing timestamp.
		jobID := strings.Join(parts[1:len(parts)-1], "_")

		link := CronRunLink{
			SessionID: s.ID,
			JobID:     jobID,
		}
		if job, ok := jobMap[jobID]; ok {
			link.Job = job
		} else {
			link.Orphan = true
		}
		links = append(links, link)
	}
	return links
}
