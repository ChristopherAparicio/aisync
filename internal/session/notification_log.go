package session

import "time"

type NotificationLogEntry struct {
	ID             int64
	EventType      string
	Severity       string
	Project        string
	Title          string
	Summary        string
	PayloadJSON    string
	DispatchedAt   time.Time
	AcknowledgedAt *time.Time
	AcknowledgedBy string
	DedupKey       string
}

func (n NotificationLogEntry) IsAcknowledged() bool {
	return n.AcknowledgedAt != nil
}

type NotificationLogFilter struct {
	Since           time.Time
	Until           time.Time
	EventTypes      []string
	Severities      []string
	Projects        []string
	OnlyUnack       bool
	Limit           int
	Offset          int
}
