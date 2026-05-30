package hermes

// stripSentinel removes the 2-byte Hermes internal prefix (\x00\x01) from s.
// Content stored by Hermes may carry this prefix; callers strip it during mapping.
// If the prefix is absent the original string is returned unchanged.
func stripSentinel(s string) string {
	if len(s) >= 2 && s[0] == '\x00' && s[1] == '\x01' {
		return s[2:]
	}
	return s
}
