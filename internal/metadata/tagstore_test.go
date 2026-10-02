package metadata

import (
	"context"
	"testing"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

func TestTagStoreServesRepeatedReadsFromMemory(t *testing.T) {
	path := newTestMP3(t)
	tagTestFile(t, path, "Before", "Artist")
	var s tagStore

	if _, err := s.read(path); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Changed behind the store's back: a second read that went to disk
	// would see it.
	writeTestTags(t, path, map[string][]string{taglib.Title: {"Behind"}})

	tags, err := s.read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := FirstTag(tags, taglib.Title); got != "Before" {
		t.Errorf("Title = %q, want %q from memory", got, "Before")
	}
}

func TestTagStoreReadsAgainAfterAWrite(t *testing.T) {
	path := newTestMP3(t)
	tagTestFile(t, path, "Before", "Artist")
	var s tagStore

	if _, err := s.read(path); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := s.write(path, map[string][]string{taglib.Title: {"After"}}); err != nil {
		t.Fatalf("write: %v", err)
	}

	tags, err := s.read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := FirstTag(tags, taglib.Title); got != "After" {
		t.Errorf("Title = %q, want %q", got, "After")
	}
	if got := FirstTag(tags, taglib.Artist); got != "Artist" {
		t.Errorf("Artist = %q: a write must leave the other tags alone", got)
	}
}

// The album-first phase writes the track number; the per-file phase reads the
// file's tags again to protect it. Served a copy from before that write, it
// would let the provider's number overwrite the album's.
func TestResolve_PerFilePhaseKeepsAlbumFirstTrackNumber(t *testing.T) {
	path := newTestMP3(t)
	writeTestTags(t, path, map[string][]string{
		taglib.Title: {"TRUST!"}, taglib.Artist: {"JPEGMAFIA"}, taglib.Album: {"LP!"},
	})

	ar := &mockAlbumResolver{found: true, tracklist: Tracklist{
		Tracks: []ReleaseTrack{{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"}},
	}}
	provider := &mockProvider{name: "p", results: []TrackInfo{
		{Title: "TRUST!", Artist: "JPEGMAFIA", Album: "LP!", TrackNumber: 9, DiscNumber: 2},
	}}
	r := NewResolver([]Provider{provider}, logger.New(false), 0).WithAlbumResolver(ar)
	if err := r.Resolve(context.Background(), []string{path}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := readTestTag(t, path, taglib.TrackNumber); got != "1" {
		t.Errorf("TrackNumber = %q, want %q from the album-first phase", got, "1")
	}
	if got := readTestTag(t, path, taglib.DiscNumber); got != "1" {
		t.Errorf("DiscNumber = %q, want %q from the album-first phase", got, "1")
	}
}

func TestAlbumArtistFallback(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string][]string
		artist   string
		want     string
	}{
		{"file already has one", map[string][]string{taglib.AlbumArtist: {"Band"}}, "Band, Guest", ""},
		{"primary of the artist being written", nil, "Band, Guest", "Band"},
		{"primary of the file's artist", map[string][]string{taglib.Artist: {"Solo, Other"}}, "", "Solo"},
		{"no artist anywhere", nil, "", ""},
	}
	for _, tt := range tests {
		if got := albumArtistFallback(tt.existing, tt.artist); got != tt.want {
			t.Errorf("%s: albumArtistFallback = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestWritePositionalTags_WritesTrackAndDisc(t *testing.T) {
	p := newTestMP3(t)

	if err := (&tagStore{}).writePositional(p, 5, 2); err != nil {
		t.Fatalf("writePositional: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := FirstTag(tags, taglib.TrackNumber); got != "5" {
		t.Errorf("TrackNumber = %q, want %q", got, "5")
	}
	if got := FirstTag(tags, taglib.DiscNumber); got != "2" {
		t.Errorf("DiscNumber = %q, want %q", got, "2")
	}
}

func TestWritePositionalTags_SkipsZeroValues(t *testing.T) {
	p := newTestMP3(t)
	writeTestTags(t, p, map[string][]string{taglib.TrackNumber: {"3"}})

	// disc = 0 means "unknown", should not write
	if err := (&tagStore{}).writePositional(p, 4, 0); err != nil {
		t.Fatalf("writePositional: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := FirstTag(tags, taglib.TrackNumber); got != "4" {
		t.Errorf("TrackNumber = %q, want %q", got, "4")
	}
	if got := FirstTag(tags, taglib.DiscNumber); got != "" {
		t.Errorf("DiscNumber = %q, want empty (zero not written)", got)
	}
}
