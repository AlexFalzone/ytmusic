package downloader

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePlaylistReadsEntriesInOrder(t *testing.T) {
	out := `{"webpage_url": "https://music.youtube.com/watch?v=a1", "title": "Over My Dead Body", "playlist_index": 1, "playlist_count": 19, "playlist_title": "Album - Take Care (Deluxe)", "playlist_id": "OLAK5uy_x"}

{"webpage_url": "https://music.youtube.com/watch?v=b2", "title": "Shot For Me", "playlist_index": 2, "playlist_count": 19, "playlist_title": "Album - Take Care (Deluxe)", "playlist_id": "OLAK5uy_x"}
`
	pl, err := parsePlaylist(strings.NewReader(out), nil)
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
	pl, err := parsePlaylist(strings.NewReader(`{"webpage_url": "https://www.youtube.com/watch?v=jNQXAC9IVRw", "title": "Me at the zoo"}`+"\n"), nil)
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
	pl, err := parsePlaylist(strings.NewReader(out+"\n"), nil)
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if got := pl.Entries[0].URL; got != "https://soundcloud.com/forss/city-ports" {
		t.Errorf("URL = %q, want the entry's own page", got)
	}
}

func TestParsePlaylistFallsBackToTheEntryURL(t *testing.T) {
	pl, err := parsePlaylist(strings.NewReader(`{"url": "https://example.com/track/1"}`+"\n"), nil)
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if got := pl.Entries[0].URL; got != "https://example.com/track/1" {
		t.Errorf("URL = %q, want the url field", got)
	}
}

// These URLs come from the page yt-dlp read, not from us, and end up on its command line.
func TestParsePlaylistRejectsAnEntryThatIsNotAWebURL(t *testing.T) {
	for _, bad := range []string{"--exec=touch /tmp/pwned", "-x", "file:///etc/passwd", "ftp://example.com/a", "https://", "example.com/a"} {
		line, err := json.Marshal(map[string]string{"webpage_url": bad})
		if err != nil {
			t.Fatal(err)
		}
		if pl, err := parsePlaylist(strings.NewReader(string(line)+"\n"), nil); err == nil {
			t.Errorf("%q accepted: %+v", bad, pl.Entries)
		}
	}
}

// A page on an allowed site must not send yt-dlp to one that is not.
func TestParsePlaylistHoldsEntriesToAllowedHosts(t *testing.T) {
	allowed := []string{"youtube.com"}
	inside := `{"webpage_url": "https://music.youtube.com/watch?v=a"}` + "\n"
	outside := `{"webpage_url": "http://192.168.1.1/admin"}` + "\n"

	if _, err := parsePlaylist(strings.NewReader(inside), allowed); err != nil {
		t.Errorf("entry on an allowed host: %v", err)
	}
	if _, err := parsePlaylist(strings.NewReader(inside+outside), allowed); err == nil {
		t.Error("entry on another host accepted")
	}
	if _, err := parsePlaylist(strings.NewReader(outside), nil); err != nil {
		t.Errorf("no allowed_hosts means any site: %v", err)
	}
}

func TestParsePlaylistRejectsAnEntryWithoutAURL(t *testing.T) {
	if _, err := parsePlaylist(strings.NewReader(`{"title": "Nowhere"}`+"\n"), nil); err == nil {
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
	if _, err := parsePlaylist(strings.NewReader("https://www.youtube.com/watch?v=x\n"), nil); err == nil {
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
