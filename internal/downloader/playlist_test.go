package downloader

import (
	"strings"
	"testing"
)

func TestParsePlaylistReadsEntriesInOrder(t *testing.T) {
	out := `{"webpage_url": "https://music.youtube.com/watch?v=a1", "title": "Over My Dead Body", "playlist_index": 1, "playlist_count": 19, "playlist_title": "Album - Take Care (Deluxe)", "playlist_id": "OLAK5uy_x"}

{"webpage_url": "https://music.youtube.com/watch?v=b2", "title": "Shot For Me", "playlist_index": 2, "playlist_count": 19, "playlist_title": "Album - Take Care (Deluxe)", "playlist_id": "OLAK5uy_x"}
`
	pl, err := parsePlaylist(strings.NewReader(out))
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}

	if pl.ID != "OLAK5uy_x" || pl.Title != "Album - Take Care (Deluxe)" || pl.Count != 19 {
		t.Errorf("playlist = %q %q %d, want OLAK5uy_x, Album - Take Care (Deluxe), 19", pl.ID, pl.Title, pl.Count)
	}
	want := []Entry{
		{URL: "https://www.youtube.com/watch?v=a1", Title: "Over My Dead Body", Position: 1},
		{URL: "https://www.youtube.com/watch?v=b2", Title: "Shot For Me", Position: 2},
	}
	if len(pl.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(pl.Entries), len(want))
	}
	for i := range want {
		if pl.Entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, pl.Entries[i], want[i])
		}
	}
}

// yt-dlp prints only the fields a single video has.
func TestParsePlaylistSingleVideo(t *testing.T) {
	pl, err := parsePlaylist(strings.NewReader(`{"webpage_url": "https://www.youtube.com/watch?v=jNQXAC9IVRw", "title": "Me at the zoo"}` + "\n"))
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if len(pl.Entries) != 1 || pl.Entries[0].URL != "https://www.youtube.com/watch?v=jNQXAC9IVRw" || pl.Entries[0].Position != 0 {
		t.Errorf("entries = %+v, want one entry without a position", pl.Entries)
	}
	if _, ok := pl.Album(); ok {
		t.Error("a single video is not an album")
	}
}

// An entry's id means nothing outside YouTube: SoundCloud's are plain numbers.
func TestParsePlaylistKeepsTheSiteOfAnEntry(t *testing.T) {
	out := `{"id": "290", "url": "https://soundcloud.com/forss/city-ports", "webpage_url": "https://soundcloud.com/forss/city-ports", "playlist_index": 1}`
	pl, err := parsePlaylist(strings.NewReader(out + "\n"))
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if got := pl.Entries[0].URL; got != "https://soundcloud.com/forss/city-ports" {
		t.Errorf("URL = %q, want the entry's own page", got)
	}
}

func TestParsePlaylistFallsBackToTheEntryURL(t *testing.T) {
	pl, err := parsePlaylist(strings.NewReader(`{"url": "https://example.com/track/1"}` + "\n"))
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if got := pl.Entries[0].URL; got != "https://example.com/track/1" {
		t.Errorf("URL = %q, want the url field", got)
	}
}

func TestParsePlaylistRejectsAnEntryWithoutAURL(t *testing.T) {
	if _, err := parsePlaylist(strings.NewReader(`{"title": "Nowhere"}` + "\n")); err == nil {
		t.Error("expected an error for an entry nothing can download")
	}
}

func TestPlaylistURLsDropDuplicatesInOrder(t *testing.T) {
	pl := Playlist{Entries: []Entry{{URL: "b"}, {URL: "a"}, {URL: "b"}, {URL: "c"}}}
	if got := strings.Join(pl.URLs(), " "); got != "b a c" {
		t.Errorf("URLs() = %q, want b a c", got)
	}
}

func TestParsePlaylistRejectsGarbage(t *testing.T) {
	if _, err := parsePlaylist(strings.NewReader("https://www.youtube.com/watch?v=x\n")); err == nil {
		t.Error("expected an error for a line that is not JSON")
	}
}

func TestPlaylistAlbum(t *testing.T) {
	tests := []struct {
		name, id, title string
		want            string
		ok              bool
	}{
		{"album", "OLAK5uy_mXmZ7g86CVTU7i1", "Album - Take Care (Deluxe)", "Take Care (Deluxe)", true},
		{"album without the prefix", "OLAK5uy_abc", "Persona", "Persona", true},
		{"user playlist named like an album", "PLabc", "Album - Mix", "", false},
		{"no playlist", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Playlist{ID: tt.id, Title: tt.title}.Album()
			if got != tt.want || ok != tt.ok {
				t.Errorf("Album() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
