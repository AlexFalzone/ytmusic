package metadata

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ytmusic/internal/buildinfo"
	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

func pngOf(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.SetGray(0, 0, color.Gray{Y: shade})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// /a and /b are two different images; hits counts requests per path.
func artworkServer(t *testing.T) (srv *httptest.Server, a, b []byte, hits func(path string) int) {
	t.Helper()
	a, b = pngOf(t, 0), pngOf(t, 255)
	var mu sync.Mutex
	counts := make(map[string]int)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		if got := r.Header.Get("User-Agent"); got != buildinfo.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, buildinfo.UserAgent())
		}
		var img []byte
		switch r.URL.Path {
		case "/a":
			img = a
		case "/b":
			img = b
		default:
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write(img); err != nil {
			t.Errorf("write image: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, a, b, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[path]
	}
}

func readTestImage(t *testing.T, path string) []byte {
	t.Helper()
	img, err := taglib.ReadImage(path)
	if err != nil {
		t.Fatalf("read image: %v", err)
	}
	return img
}

func TestResolveFile_FallsBackToFillerArtwork(t *testing.T) {
	srv, _, b, _ := artworkServer(t)
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Artist")

	primary := &mockProvider{name: "primary", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/missing"},
	}}
	filler := &mockProvider{name: "filler", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", Genre: "Rock", ArtworkURL: srv.URL + "/b"},
	}}
	resolveOne(t, path, primary, filler)

	if got := readTestImage(t, path); !bytes.Equal(got, b) {
		t.Errorf("embedded image is %d bytes, want the filler's %d", len(got), len(b))
	}
}

func TestResolveFile_KeepsPrimaryArtwork(t *testing.T) {
	srv, a, _, hits := artworkServer(t)
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Artist")

	primary := &mockProvider{name: "primary", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/a"},
	}}
	filler := &mockProvider{name: "filler", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", Genre: "Rock", ArtworkURL: srv.URL + "/b"},
	}}
	resolveOne(t, path, primary, filler)

	if got := readTestImage(t, path); !bytes.Equal(got, a) {
		t.Errorf("embedded image is %d bytes, want the primary's %d", len(got), len(a))
	}
	if n := hits("/b"); n != 0 {
		t.Errorf("filler artwork downloaded %d times, want never: the primary's worked", n)
	}
}

func TestResolveFile_FingerprintMatchFallsBackToFillerArtwork(t *testing.T) {
	srv, _, b, _ := artworkServer(t)
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Artist")

	fp := &stubFingerprinter{info: TrackInfo{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/missing"}, found: true}
	filler := &mockProvider{name: "filler", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/b"},
	}}
	r := NewResolver([]Provider{filler}, logger.New(false), 0).WithFingerprinter(fp)
	if err := r.Resolve(context.Background(), []string{path}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := readTestImage(t, path); !bytes.Equal(got, b) {
		t.Errorf("embedded image is %d bytes, want the filler's %d", len(got), len(b))
	}
}

// The MusicBrainz filler offers the very URL that just failed: it must not keep a later provider's artwork out.
func TestResolveFile_DroppedArtworkOfferedAgainIsSkipped(t *testing.T) {
	srv, _, b, _ := artworkServer(t)
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Artist")

	fp := &stubFingerprinter{info: TrackInfo{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/missing"}, found: true}
	sameRelease := &mockProvider{name: "musicbrainz", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/missing"},
	}}
	later := &mockProvider{name: "itunes", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/b"},
	}}
	r := NewResolver([]Provider{sameRelease, later}, logger.New(false), 0).WithFingerprinter(fp)
	if err := r.Resolve(context.Background(), []string{path}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := readTestImage(t, path); !bytes.Equal(got, b) {
		t.Errorf("embedded image is %d bytes, want the later provider's %d", len(got), len(b))
	}
}

// The first filler completes every field but its artwork is broken: gap filling must not stop there.
func TestResolveFile_BrokenFillerArtworkFallsBackToTheNext(t *testing.T) {
	srv, _, b, _ := artworkServer(t)
	path := newTestMP3(t)
	tagTestFile(t, path, "Song", "Artist")

	primary := &mockProvider{name: "primary", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/missing"},
	}}
	complete := &mockProvider{name: "complete", results: []TrackInfo{{
		Title: "Song", Artist: "Artist", Album: "Album", Genre: "Rock", TrackNumber: 1, DiscNumber: 1,
		Year: 2020, ISRC: "USXX12345678", ArtworkURL: srv.URL + "/also-missing",
	}}}
	last := &mockProvider{name: "last", results: []TrackInfo{
		{Title: "Song", Artist: "Artist", Album: "Album", ArtworkURL: srv.URL + "/b"},
	}}
	resolveOne(t, path, primary, complete, last)

	if got := readTestImage(t, path); !bytes.Equal(got, b) {
		t.Errorf("embedded image is %d bytes, want the last provider's %d", len(got), len(b))
	}
}
