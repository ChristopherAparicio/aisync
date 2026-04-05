package identity

import (
	"strings"
)

// MatchResult holds the result of comparing a Git user name against a Slack member.
type MatchResult struct {
	// Score is the best match score (0.0 to 1.0).
	Score float64

	// Reason describes which matching strategy produced this score.
	Reason string
}

// MatchNames compares a Git user name against a Slack member's names and email.
// It tries multiple strategies and returns the best match:
//
//  1. Exact email match (score 1.0)
//  2. Exact name match (score 1.0)
//  3. Token overlap on name parts (score based on Jaccard similarity)
//  4. Levenshtein distance on normalized names (score based on edit distance ratio)
//
// The gitName is the user's name from git config, and gitEmail is their git email.
// slackRealName, slackDisplayName, slackEmail come from the Slack profile.
func MatchNames(gitName, gitEmail, slackRealName, slackDisplayName, slackEmail string) MatchResult {
	best := MatchResult{Score: 0, Reason: "no match"}

	// Strategy 1: Exact email match (strongest signal)
	if gitEmail != "" && slackEmail != "" {
		if strings.EqualFold(gitEmail, slackEmail) {
			return MatchResult{Score: 1.0, Reason: "exact email match"}
		}
	}

	// Strategy 2: Exact name match (case-insensitive)
	gitNorm := normalizeName(gitName)
	for _, slackName := range []string{slackRealName, slackDisplayName} {
		if slackName == "" {
			continue
		}
		slackNorm := normalizeName(slackName)
		if gitNorm != "" && gitNorm == slackNorm {
			return MatchResult{Score: 1.0, Reason: "exact name match"}
		}
	}

	// Strategy 3: Token overlap (Jaccard similarity on name tokens)
	for _, slackName := range []string{slackRealName, slackDisplayName} {
		if slackName == "" {
			continue
		}
		score := tokenOverlap(gitName, slackName)
		if score > best.Score {
			best = MatchResult{Score: score, Reason: "token overlap: " + slackName}
		}
	}

	// Strategy 4: Levenshtein distance on normalized full name
	for _, slackName := range []string{slackRealName, slackDisplayName} {
		if slackName == "" {
			continue
		}
		score := levenshteinSimilarity(gitNorm, normalizeName(slackName))
		if score > best.Score {
			best = MatchResult{Score: score, Reason: "name similarity: " + slackName}
		}
	}

	// Strategy 5: Email username vs name tokens
	// e.g., git email "christophe.aparicio@company.com" → username "christophe.aparicio"
	// → tokens ["christophe", "aparicio"] → compare with Slack name tokens
	if gitEmail != "" {
		emailUser := extractEmailUsername(gitEmail)
		if emailUser != "" {
			for _, slackName := range []string{slackRealName, slackDisplayName} {
				if slackName == "" {
					continue
				}
				score := tokenOverlap(emailUser, slackName)
				// Slightly discount email-based matches vs direct name matches
				score *= 0.9
				if score > best.Score {
					best = MatchResult{Score: score, Reason: "email username vs name: " + slackName}
				}
			}
		}
	}

	return best
}

// normalizeName lowercases and trims a name, collapsing whitespace.
func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Collapse multiple spaces
	parts := strings.Fields(name)
	return strings.Join(parts, " ")
}

// tokenize splits a name into lowercase tokens, splitting on spaces, dots,
// dashes, and underscores. This handles names like "John.Doe", "john-doe",
// "john_doe" and "John Doe" uniformly.
func tokenize(name string) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Replace common separators with spaces
	r := strings.NewReplacer(".", " ", "-", " ", "_", " ", "@", " ")
	name = r.Replace(name)
	tokens := strings.Fields(name)
	// Deduplicate
	seen := make(map[string]bool, len(tokens))
	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !seen[t] && len(t) > 0 {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}

// tokenOverlap computes the Jaccard similarity between the tokens of two names.
// Returns a score between 0.0 and 1.0.
func tokenOverlap(name1, name2 string) float64 {
	tokens1 := tokenize(name1)
	tokens2 := tokenize(name2)

	if len(tokens1) == 0 || len(tokens2) == 0 {
		return 0
	}

	set2 := make(map[string]bool, len(tokens2))
	for _, t := range tokens2 {
		set2[t] = true
	}

	intersection := 0
	for _, t := range tokens1 {
		if set2[t] {
			intersection++
		}
	}

	if intersection == 0 {
		return 0
	}

	// Jaccard = |A ∩ B| / |A ∪ B|
	union := len(tokens1) + len(tokens2) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

// levenshteinSimilarity computes the similarity between two strings based on
// Levenshtein edit distance. Returns a score between 0.0 and 1.0.
func levenshteinSimilarity(a, b string) float64 {
	if a == "" && b == "" {
		return 0 // Both empty → no meaningful comparison
	}
	if a == "" || b == "" {
		return 0
	}

	dist := levenshteinDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	return 1.0 - float64(dist)/float64(maxLen)
}

// levenshteinDistance computes the edit distance between two strings.
// Uses the iterative Wagner-Fischer algorithm with O(min(m,n)) space.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Ensure a is the shorter string for space optimization
	if len(a) > len(b) {
		a, b = b, a
	}

	prev := make([]int, len(a)+1)
	curr := make([]int, len(a)+1)

	// Initialize base case
	for i := range prev {
		prev[i] = i
	}

	for j := 1; j <= len(b); j++ {
		curr[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[i] = min3(
				curr[i-1]+1,    // insertion
				prev[i]+1,      // deletion
				prev[i-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[len(a)]
}

// min3 returns the minimum of three integers.
func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// extractEmailUsername extracts the username part of an email address.
// e.g., "john.doe@company.com" → "john.doe"
func extractEmailUsername(email string) string {
	idx := strings.Index(email, "@")
	if idx <= 0 {
		return ""
	}
	return email[:idx]
}
