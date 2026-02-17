package show

import "testing"

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "0", 0},
		{"small", "100", 100},
		{"thousands", "1,234", 1234},
		{"tens of thousands", "57,000", 57000},
		{"millions", "1,234,567", 1234567},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatNumber(tt.input)
			if got != tt.want {
				t.Errorf("formatNumber(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
