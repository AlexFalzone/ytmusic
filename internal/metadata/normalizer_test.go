package metadata

import "testing"

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		artist     string
		wantTitle  string
		wantArtist string
	}{
		{
			name:       "clean title and artist",
			title:      "Blinding Lights",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "official video parentheses",
			title:      "Blinding Lights (Official Video)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "official music video brackets",
			title:      "Blinding Lights [Official Music Video]",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "official audio",
			title:      "Blinding Lights (Official Audio)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "lyrics suffix",
			title:      "Blinding Lights (Lyrics)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "lyric video",
			title:      "Blinding Lights (Official Lyric Video)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "visualizer",
			title:      "Blinding Lights (Visualizer)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "visual",
			title:      "Blinding Lights (Visual)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "HD suffix",
			title:      "Blinding Lights (HD)",
			artist:     "The Weeknd",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "featuring in title",
			title:      "HUMBLE. (feat. Jay Rock)",
			artist:     "Kendrick Lamar",
			wantTitle:  "HUMBLE.",
			wantArtist: "Kendrick Lamar",
		},
		{
			name:       "ft. in title",
			title:      "Locked Out Of Heaven (ft. Bruno Mars)",
			artist:     "Some Artist",
			wantTitle:  "Locked Out Of Heaven",
			wantArtist: "Some Artist",
		},
		{
			name:       "VEVO artist suffix",
			title:      "Blinding Lights",
			artist:     "TheWeekndVEVO",
			wantTitle:  "Blinding Lights",
			wantArtist: "TheWeeknd",
		},
		{
			name:       "VEVO lowercase",
			title:      "Blinding Lights",
			artist:     "TheWeekndvevo",
			wantTitle:  "Blinding Lights",
			wantArtist: "TheWeeknd",
		},
		{
			name:       "artist dash title no artist metadata",
			title:      "The Weeknd - Blinding Lights",
			artist:     "",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "artist dash title with video suffix no artist",
			title:      "The Weeknd - Blinding Lights (Official Video)",
			artist:     "",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "multiple suffixes",
			title:      "Song Name (feat. Other) (Official Video) [HD]",
			artist:     "Main Artist",
			wantTitle:  "Song Name",
			wantArtist: "Main Artist",
		},
		{
			name:       "explicit tag",
			title:      "WAP (Explicit)",
			artist:     "Cardi B",
			wantTitle:  "WAP",
			wantArtist: "Cardi B",
		},
		{
			name:       "empty title",
			title:      "",
			artist:     "Some Artist",
			wantTitle:  "",
			wantArtist: "Some Artist",
		},
		{
			name:       "whitespace cleanup",
			title:      "  Blinding Lights  ",
			artist:     "  The Weeknd  ",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
		{
			name:       "em dash separator",
			title:      "The Weeknd — Blinding Lights",
			artist:     "",
			wantTitle:  "Blinding Lights",
			wantArtist: "The Weeknd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeQuery(tt.title, tt.artist)
			if got.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.Artist != tt.wantArtist {
				t.Errorf("artist = %q, want %q", got.Artist, tt.wantArtist)
			}
		})
	}
}
