package metadata

import "testing"

func TestParseYear(t *testing.T) {
	tests := []struct {
		date string
		want int
	}{
		{"2020-03-20", 2020},
		{"2020-03", 2020},
		{"2020", 2020},
		{"2019-11-29T08:00:00Z", 2019},
		{"", 0},
		{"19", 0},
		{"abc", 0},
		// Sscanf read the leading digits and called this year 20.
		{"20x9-01-01", 0},
	}
	for _, tt := range tests {
		if got := ParseYear(tt.date); got != tt.want {
			t.Errorf("ParseYear(%q) = %d, want %d", tt.date, got, tt.want)
		}
	}
}
