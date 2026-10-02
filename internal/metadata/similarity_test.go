package metadata

import (
	"math"
	"testing"
)

func TestJaroWinkler(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		// Reference values from Winkler's paper.
		{"martha", "marhta", 0.961},
		{"dwayne", "duane", 0.840},
		{"dixon", "dicksonx", 0.813},
		{"same", "same", 1.0},
		{"", "", 1.0},
		{"abc", "", 0.0},
	}
	for _, tt := range tests {
		if got := jaroWinkler(tt.a, tt.b); math.Abs(got-tt.want) > 0.001 {
			t.Errorf("jaroWinkler(%q, %q) = %.3f, want %.3f", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestTokensMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"light", "lights", true},     // plural
		{"box", "boxes", true},        // plural
		{"blinding", "blindng", true}, // one-letter typo, JW 0.975
		{"walking", "talking", false}, // another word, JW 0.905
		{"love", "live", false},       // another word, JW 0.850
		{"me", "we", false},           // too short to be fuzzy
		{"i", "is", false},            // too short for the plural rule too
		// One letter apart, but each pair is two different words. Edit distance
		// alone cannot tell them apart: every one of these scores above 0.92.
		{"lock", "clock", false},
		{"ever", "never", false},
		{"word", "world", false},
		{"star", "start", false},
		{"alone", "along", false},
		{"thing", "think", false},
		{"chance", "change", false},
		{"storm", "story", false},
	}
	for _, tt := range tests {
		if got := tokensMatch(tt.a, tt.b); got != tt.want {
			t.Errorf("tokensMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Perché", "perche"},
		{"Beyoncé", "beyonce"},
		{"Città", "citta"},
		{"Simon & Garfunkel", "simon and garfunkel"},
		{"Don't Stop Me Now", "dont stop me now"},
		{"  Blinding   Lights ", "blinding lights"},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSimilarityOnNormalizedTitles(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"Blinding Lights", "Blinding Light", 1.0},
		{"Perché", "Perche", 1.0},
		{"Simon & Garfunkel", "Simon and Garfunkel", 1.0},
		{"Walking on Sunshine", "Talking on Sunshine", 2.0 / 3},
		// Unrelated titles must stay at zero: whole-string Jaro-Winkler would
		// have put this pair at 0.63, next to the 0.7 threshold.
		{"Blinding Lights", "Bohemian Rhapsody", 0.0},
	}
	for _, tt := range tests {
		got := similarity(normalize(tt.a), normalize(tt.b))
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("similarity(%q, %q) = %.4f, want %.4f", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"blinding lights", "blinding lights", 1.0},
		{"", "", 1.0},
		{"something", "", 0.0},
		{"", "something", 0.0},
	}

	for _, tt := range tests {
		got := similarity(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("similarity(%q, %q) = %.4f, want %.4f", tt.a, tt.b, got, tt.want)
		}
	}
}
