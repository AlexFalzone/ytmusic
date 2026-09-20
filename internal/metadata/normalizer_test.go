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
			got, _ := NormalizeQuery(tt.title, tt.artist)
			if got.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.Artist != tt.wantArtist {
				t.Errorf("artist = %q, want %q", got.Artist, tt.wantArtist)
			}
		})
	}
}

func TestCleanTitle(t *testing.T) {
	tests := []struct {
		raw         string
		wantBase    string
		wantVersion string
	}{
		{"Blinding Lights", "Blinding Lights", ""},
		{"Blinding Lights (Official Video)", "Blinding Lights", ""},
		{"Peaches (feat. Daniel Caesar & Giveon)", "Peaches", ""},
		{"Stay (with Justin Bieber)", "Stay", ""},
		{"Song ft. Someone", "Song", ""},
		{"Here Comes the Sun - Remastered 2019", "Here Comes the Sun", ""},
		{"Yesterday (2009 Remaster)", "Yesterday", ""},
		{"Blinding Lights (Sped Up)", "Blinding Lights", "sped up"},
		{"Blinding Lights [Slowed + Reverb]", "Blinding Lights", "slowed"},
		{"Don't Stop Me Now - Live at Wembley 1986", "Don't Stop Me Now", "live"},
		{"Levels (Skrillex Remix)", "Levels", "remix"},
		{"Song - Radio Edit", "Song", "edit"},
		{"Song (Acoustic Version)", "Song", "acoustic"},
		{"Song (Live) [Acoustic]", "Song", "acoustic+live"},
		// Suffixes stack, and Spotify joins them with a semicolon.
		{"Song - Live - Remastered 2011", "Song", "live"},
		{"Comfortably Numb - Live; 2000 Remaster", "Comfortably Numb", "live"},
		// Stops at the first suffix that is not a marker.
		{"Song - Live - Studio Chat", "Song - Live - Studio Chat", ""},
		// Titles that merely contain a marker word are not variants.
		{"Live Forever", "Live Forever", ""},
		{"Remix to Ignition", "Remix to Ignition", ""},
		{"Live and Let Die", "Live and Let Die", ""},
		{"Anti-Hero", "Anti-Hero", ""},
		{"(I Just) Died in Your Arms", "(I Just) Died in Your Arms", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			base, v := cleanTitle(tt.raw)
			if base != tt.wantBase {
				t.Errorf("base = %q, want %q", base, tt.wantBase)
			}
			if v.Key != tt.wantVersion {
				t.Errorf("version = %q, want %q", v.Key, tt.wantVersion)
			}
		})
	}
}

func TestCleanTitleKeepsVariantLabel(t *testing.T) {
	tests := []struct{ raw, wantLabel string }{
		{"Levels (Skrillex Remix)", "Skrillex Remix"},
		{"Song (Live) [Acoustic]", "Live, Acoustic"},
	}
	for _, tt := range tests {
		if _, v := cleanTitle(tt.raw); v.Label != tt.wantLabel {
			t.Errorf("cleanTitle(%q) label = %q, want %q", tt.raw, v.Label, tt.wantLabel)
		}
	}
}

func TestNormalizeQueryReturnsVersion(t *testing.T) {
	tests := []struct {
		title, artist         string
		wantTitle, wantArtist string
		wantVersion           string
	}{
		{"Blinding Lights (Sped Up)", "The Weeknd", "Blinding Lights", "The Weeknd", "sped up"},
		// The first dash separates the artist, the last one the variant.
		{"The Weeknd - Blinding Lights - Live", "", "Blinding Lights", "The Weeknd", "live"},
		// A song called "Live Forever" is not a live version.
		{"Oasis - Live Forever", "", "Live Forever", "Oasis", ""},
	}
	for _, tt := range tests {
		q, v := NormalizeQuery(tt.title, tt.artist)
		if q.Title != tt.wantTitle || q.Artist != tt.wantArtist || v.Key != tt.wantVersion {
			t.Errorf("NormalizeQuery(%q, %q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.title, tt.artist, q.Title, q.Artist, v.Key, tt.wantTitle, tt.wantArtist, tt.wantVersion)
		}
	}
}

// Without an artist tag, a hyphenated title is not "Artist - Song".
func TestNormalizeQueryKeepsHyphenatedTitle(t *testing.T) {
	q, _ := NormalizeQuery("Anti-Hero", "")

	if q.Title != "Anti-Hero" || q.Artist != "" {
		t.Errorf("got title %q artist %q, want %q and none", q.Title, q.Artist, "Anti-Hero")
	}
}
