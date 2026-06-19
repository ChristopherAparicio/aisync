package session

// RetentionTierStorageStats reports storage usage for one retention tier.
type RetentionTierStorageStats struct {
	Tier     RetentionTier `json:"tier"`
	Sessions int           `json:"sessions"`
	Bytes    int           `json:"bytes"`
}
