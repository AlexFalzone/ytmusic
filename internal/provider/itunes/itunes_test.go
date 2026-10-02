package itunes

import "testing"

func TestParseResultsYear(t *testing.T) {
	tests := []struct {
		date string
		want int
	}{
		{date: "2019-11-29T08:00:00Z", want: 2019},
		{date: "", want: 0},
		{date: "19", want: 0},
		// Sscanf once read this as year 20.
		{date: "20x9-01-01", want: 0},
	}

	for _, tt := range tests {
		got := parseResults([]resultItem{{ReleaseDate: tt.date}})
		if len(got) != 1 {
			t.Fatalf("%q: got %d results, want 1", tt.date, len(got))
		}
		if got[0].Year != tt.want {
			t.Errorf("%q: Year = %d, want %d", tt.date, got[0].Year, tt.want)
		}
	}
}
