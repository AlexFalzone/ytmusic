package metadata

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"ytmusic/internal/logger"
	"ytmusic/internal/testaudio"

	"go.senan.xyz/taglib"
)

func tracklist(id, status, date string, titles ...string) Tracklist {
	tl := Tracklist{ID: id, Status: status, Date: date}
	for i, title := range titles {
		tl.Tracks = append(tl.Tracks, ReleaseTrack{TrackNumber: i + 1, DiscNumber: 1, Title: title})
	}
	return tl
}

var divineFeminine = []string{
	"Congratulations (feat. Bilal)", "Dang! (feat. Anderson .Paak)", "Stay", "Skin", "Cinderella (feat. Ty Dolla $ign)",
	"Planet God Damn (feat. Njomza)", "Soulmate", "We (feat. CeeLo Green)", "My Favorite Part",
	"God Is Fair, Sexy Nasty (feat. Kendrick Lamar)", "Grand", "Channel 13", "Butterflies",
}

func TestMatchReleaseToleratesOneTitleOfNineteen(t *testing.T) {
	album := []string{"Over My Dead Body", "Shot For Me", "Headlines (Explicit Version)", "Crew Love", "Take Care",
		"Marvins Room", "Buried Alive Interlude", "Under Ground Kings", "We'll Be Fine", "Make Me Proud", "Lord Knows",
		"Cameras / Good Ones Go Interlude (Medley)", "Doing It Wrong", "The Real Her", "Look What You've Done",
		"HYFR (Hell Ya Fucking Right)", "Practice", "The Ride", "The Motto"}
	release := tracklist("deluxe", "Official", "2012-03-20", "Over My Dead Body", "Shot for Me", "Headlines", "Crew Love",
		"Take Care", "Marvins Room", "Buried Alive Interlude", "Under Ground Kings", "We’ll Be Fine", "Make Me Proud",
		"Lord Knows", "Cameras / Good Ones Go Interlude (medley)", "Doing It Wrong", "The Real Her",
		"Look What You’ve Done", "HYFR (Hell Ya Fucking Right)", "Practice", "The Ride", "The Motto")

	got, ok := matchRelease(album, []Tracklist{release})
	if !ok || got.ID != "deluxe" {
		t.Errorf("matchRelease = %q, %v; want the deluxe release despite one title that differs", got.ID, ok)
	}
}

// A bootleg sharing the first ten titles is another edition.
func TestMatchReleaseRejectsAnotherEditionWithTheSameCount(t *testing.T) {
	bootleg := tracklist("bootleg", "Bootleg", "2019", "Congratulations", "Dang!", "Stay", "Skin", "Cinderella",
		"Planet God Damn", "Soulmate", "We", "My Favorite Part", "God is Fair, Sexy Nasty",
		"Watch the Sun Come Up", "Say Goodbye", "The Sun Room")

	if got, ok := matchRelease(divineFeminine, []Tracklist{bootleg}); ok {
		t.Errorf("matchRelease chose %q: three titles of thirteen differ", got.ID)
	}
}

func TestMatchReleaseRejectsReversedOrder(t *testing.T) {
	album := []string{"BLOOD.", "DNA.", "YAH.", "ELEMENT."}
	reversed := tracklist("collectors", "Official", "2017-12-08", "ELEMENT.", "YAH.", "DNA.", "BLOOD.")

	if got, ok := matchRelease(album, []Tracklist{reversed}); ok {
		t.Errorf("matchRelease chose %q: same titles, opposite order", got.ID)
	}
}

func TestMatchReleaseRejectsAnotherTrackCount(t *testing.T) {
	standard := tracklist("standard", "Official", "2016-09-16", divineFeminine[:10]...)

	if got, ok := matchRelease(divineFeminine, []Tracklist{standard}); ok {
		t.Errorf("matchRelease chose %q: 10 tracks, the album has 13", got.ID)
	}
}

func TestMatchReleaseIgnoresUnknownTitles(t *testing.T) {
	album := []string{"Stay", "", "Skin", ""}
	release := tracklist("r", "Official", "", "Stay", "Anything", "Skin", "Else")

	if _, ok := matchRelease(album, []Tracklist{release}); !ok {
		t.Error("the two known titles match: positions without a title must not count against the release")
	}
}

func TestMatchReleaseTieBreak(t *testing.T) {
	album := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India", "Juliett"}
	oneOff := append(append([]string{}, album[:9]...), "Kilo")
	tests := []struct {
		name       string
		candidates []Tracklist
		want       string
	}{
		{"higher ratio first", []Tracklist{
			tracklist("partial", "Official", "2016", oneOff...),
			tracklist("full", "Bootleg", "2019", album...),
		}, "full"},
		{"official over bootleg", []Tracklist{
			tracklist("boot", "Bootleg", "2001", album...),
			tracklist("off", "Official", "2016", album...),
		}, "off"},
		{"earliest date", []Tracklist{
			tracklist("late", "Official", "2020-05", album...),
			tracklist("early", "Official", "2016-09-16", album...),
		}, "early"},
		{"a date over none", []Tracklist{
			tracklist("undated", "Official", "", album...),
			tracklist("dated", "Official", "2020", album...),
		}, "dated"},
		{"explicit over clean", []Tracklist{
			{ID: "clean", Status: "Official", Date: "2011", Disambiguation: "deluxe, clean", Tracks: tracklist("", "", "", album...).Tracks},
			{ID: "explicit", Status: "Official", Date: "2012", Disambiguation: "deluxe, explicit", Tracks: tracklist("", "", "", album...).Tracks},
		}, "explicit"},
		{"a full date over a year alone", []Tracklist{
			tracklist("year", "Official", "2012", album...),
			tracklist("day", "Official", "2012-03-20", album...),
		}, "day"},
		{"an earlier year over a full date", []Tracklist{
			tracklist("later", "Official", "2012-03-20", album...),
			tracklist("earlier", "Official", "2011", album...),
		}, "earlier"},
		{"lowest ID last", []Tracklist{
			tracklist("b", "Official", "2016", album...),
			tracklist("a", "Official", "2016", album...),
		}, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRelease(album, tt.candidates)
			if !ok || got.ID != tt.want {
				t.Errorf("matchRelease = %q, %v; want %q", got.ID, ok, tt.want)
			}
		})
	}
}

type fakeFinder struct {
	byTitle map[string][]Tracklist
	queries []string
}

func (f *fakeFinder) FindReleases(_ context.Context, title, artist string, tracks int) ([]Tracklist, error) {
	f.queries = append(f.queries, fmt.Sprintf("%s|%s|%d", title, artist, tracks))
	return f.byTitle[title], nil
}

// One MP3 per entry of files, keyed by position, each carrying YouTube's letterboxed thumbnail.
func newAlbum(t *testing.T, title string, tracks []string, files map[int]map[string][]string) *Album {
	t.Helper()
	dir := t.TempDir()
	album := &Album{Title: title, Tracks: tracks, Files: make(map[string]int)}
	thumb := letterboxed(t, 160, 90)
	for pos, tags := range files {
		path := testaudio.MP3(t, dir, fmt.Sprintf("%02d.mp3", pos), "0.1")
		writeTestTags(t, path, tags)
		if err := taglib.WriteImage(path, thumb); err != nil {
			t.Fatalf("write thumbnail: %v", err)
		}
		album.Files[path] = pos
	}
	return album
}

func pathAt(album *Album, pos int) string {
	for path, p := range album.Files {
		if p == pos {
			return path
		}
	}
	return ""
}

func coverServer(t *testing.T, status int) (*httptest.Server, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	cover := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if _, err := w.Write(cover); err != nil {
			t.Errorf("writing the cover: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, cover
}

func TestEditionTitles(t *testing.T) {
	tests := []struct {
		title, described string
		want             []string
	}{
		{"Take Care (Deluxe)", "Take Care", []string{"Take Care (Deluxe)", "Take Care"}},
		{"The Divine Feminine (10th Anniversary)", "The Divine Feminine (10th Anniversary)", []string{"The Divine Feminine (10th Anniversary)", "The Divine Feminine"}},
		{"Tree - Roots & Crown [New Edition]", "", []string{"Tree - Roots & Crown [New Edition]", "Tree - Roots & Crown"}},
		{"Persona", "Persona", []string{"Persona"}},
	}
	for _, tt := range tests {
		if got := editionTitles(tt.title, tt.described); !slices.Equal(got, tt.want) {
			t.Errorf("editionTitles(%q, %q) = %q, want %q", tt.title, tt.described, got, tt.want)
		}
	}
}

func TestAlbumPhaseWritesTheMatchingEdition(t *testing.T) {
	srv, cover := coverServer(t, http.StatusOK)
	album := newAlbum(t, "Take Care (Deluxe)", []string{"Over My Dead Body", "Shot For Me", "Headlines"},
		map[int]map[string][]string{
			1: {taglib.Title: {"Over My Dead Body"}, taglib.Artist: {"Drake"}, taglib.Album: {"Take Care"}, taglib.Date: {"2011-11-15"}},
			3: {taglib.Title: {"Headlines"}, taglib.Artist: {"Drake, Birdman"}, taglib.Album: {"Take Care"}, taglib.Date: {"2011-11-15"}},
		})
	release := tracklist("r1", "Official", "2012-03-20", "Over My Dead Body", "Shot for Me", "Headlines")
	release.Tracks[0].ISRC, release.Tracks[2].ISRC = "USCM51100544", "USCM51100290"
	release.ArtworkURL = srv.URL + "/front-500"
	finder := &fakeFinder{byTitle: map[string][]Tracklist{"Take Care": {release}}}

	r := NewResolver(nil, logger.New(false), 0).WithReleaseFinder(finder)
	r.resolveAlbum(t.Context(), album)

	if want := []string{"Take Care (Deluxe)|Drake|3", "Take Care|Drake|3"}; !slices.Equal(finder.queries, want) {
		t.Errorf("queries = %q, want %q", finder.queries, want)
	}
	for pos, isrc := range map[int]string{1: "USCM51100544", 3: "USCM51100290"} {
		path := pathAt(album, pos)
		want := map[string]string{
			taglib.Album: "Take Care (Deluxe)", taglib.AlbumArtist: "Drake", taglib.TrackNumber: fmt.Sprintf("%d/3", pos),
			taglib.Date: "2012-03-20", taglib.ISRC: isrc,
		}
		for key, value := range want {
			if got := readTestTag(t, path, key); got != value {
				t.Errorf("track %d %s = %q, want %q", pos, key, got, value)
			}
		}
		if !bytes.Equal(readTestImage(t, path), cover) {
			t.Errorf("track %d does not carry the release's cover", pos)
		}
	}
}

func TestAlbumPhaseWithoutAnEditionKeepsYouTubeData(t *testing.T) {
	album := newAlbum(t, "The Divine Feminine (10th Anniversary)", []string{"Congratulations", "Dang!", "Grand"},
		map[int]map[string][]string{
			1: {taglib.Title: {"Congratulations"}, taglib.Artist: {"Mac Miller, Bilal"}, taglib.Date: {"2016"}},
			2: {taglib.Title: {"Dang!"}, taglib.Artist: {"Mac Miller"}, taglib.Date: {"2016"}},
			3: {taglib.Title: {"Grand"}, taglib.Artist: {"Mac Miller"}, taglib.Date: {"2026"}},
		})

	NewResolver(nil, logger.New(false), 0).resolveAlbum(t.Context(), album)

	var first []byte
	for pos := 1; pos <= 3; pos++ {
		path := pathAt(album, pos)
		if got := readTestTag(t, path, taglib.TrackNumber); got != fmt.Sprintf("%d/3", pos) {
			t.Errorf("track %d number = %q, want %d/3", pos, got, pos)
		}
		if got := readTestTag(t, path, taglib.Date); got != "2016" {
			t.Errorf("track %d date = %q, want the album's most common date 2016", pos, got)
		}
		if got := readTestTag(t, path, taglib.AlbumArtist); got != "Mac Miller" {
			t.Errorf("track %d album artist = %q, want Mac Miller", pos, got)
		}
		img := readTestImage(t, path)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img))
		if err != nil || cfg.Width != cfg.Height {
			t.Errorf("track %d cover is %dx%d (%v), want a square", pos, cfg.Width, cfg.Height, err)
		}
		if first == nil {
			first = img
		} else if !bytes.Equal(img, first) {
			t.Errorf("track %d carries another cover than track 1", pos)
		}
	}
}

func TestAlbumPhaseFallsBackToTheThumbnailWhenTheCoverFails(t *testing.T) {
	srv, _ := coverServer(t, http.StatusNotFound)
	album := newAlbum(t, "Persona", []string{"BODY PARTS - I denti"},
		map[int]map[string][]string{1: {taglib.Title: {"BODY PARTS - I denti"}, taglib.Artist: {"Marracash"}}})
	release := tracklist("r1", "Official", "2019-10-31", "BODY PARTS - I denti")
	release.ArtworkURL = srv.URL + "/front-500"

	r := NewResolver(nil, logger.New(false), 0).WithReleaseFinder(&fakeFinder{byTitle: map[string][]Tracklist{"Persona": {release}}})
	r.resolveAlbum(t.Context(), album)

	cfg, _, err := image.DecodeConfig(bytes.NewReader(readTestImage(t, pathAt(album, 1))))
	if err != nil || cfg.Width != 90 || cfg.Height != 90 {
		t.Errorf("cover is %dx%d (%v), want the 90x90 crop of the thumbnail", cfg.Width, cfg.Height, err)
	}
}

func TestAlbumPhaseAlbumArtistWithoutAMajority(t *testing.T) {
	album := newAlbum(t, "Mix", []string{"A", "B", "C"}, map[int]map[string][]string{
		1: {taglib.Title: {"A"}, taglib.Artist: {"Alpha"}},
		2: {taglib.Title: {"B"}, taglib.Artist: {"Bravo"}},
		3: {taglib.Title: {"C"}, taglib.Artist: {"Charlie, Alpha"}},
	})

	NewResolver(nil, logger.New(false), 0).resolveAlbum(t.Context(), album)

	if got := readTestTag(t, pathAt(album, 1), taglib.AlbumArtist); got != "Various Artists" {
		t.Errorf("album artist = %q, want Various Artists: no artist leads more than half the tracks", got)
	}
}

type countingServer struct {
	*httptest.Server
	hits atomic.Int32
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.hits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(cs.Close)
	return cs
}

// A track no provider recognises must not keep yt-dlp's album name, or it lands in another folder.
func TestResolveKeepsEveryTrackOfAnAlbumDownloadInTheAlbum(t *testing.T) {
	providerArt := newCountingServer(t)
	album := newAlbum(t, "Take Care (Deluxe)", []string{"Over My Dead Body", "The Real Her"}, map[int]map[string][]string{
		1: {taglib.Title: {"Over My Dead Body"}, taglib.Artist: {"Drake"}, taglib.Album: {"Take Care"}, taglib.Date: {"2011-11-15"}},
		2: {taglib.Title: {"The Real Her"}, taglib.Artist: {"Drake, Lil Wayne, André 3000"}, taglib.Album: {"Take Care"}, taglib.Date: {"2011-11-15"}},
	})
	provider := corpusProvider{name: "p", byTitle: map[string][]TrackInfo{
		"Over My Dead Body": {{
			Title: "Over My Dead Body", Artist: "Drake", Album: "Take Care", AlbumArtist: "Drake",
			TrackNumber: 12, DiscNumber: 2, ReleaseDate: "2018", Genre: "Hip-Hop", ArtworkURL: providerArt.URL + "/cover",
		}},
	}}

	r := NewResolver([]Provider{provider}, logger.New(false), 0)
	files := []string{pathAt(album, 1), pathAt(album, 2)}
	if err := r.Resolve(t.Context(), files, album); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for pos, want := range map[int]map[string]string{
		1: {taglib.Album: "Take Care (Deluxe)", taglib.TrackNumber: "1/2", taglib.DiscNumber: "", taglib.Date: "2011-11-15", taglib.Genre: "Hip-Hop"},
		2: {taglib.Album: "Take Care (Deluxe)", taglib.TrackNumber: "2/2", taglib.DiscNumber: "", taglib.Date: "2011-11-15", taglib.AlbumArtist: "Drake"},
	} {
		for key, value := range want {
			if got := readTestTag(t, pathAt(album, pos), key); got != value {
				t.Errorf("track %d %s = %q, want %q", pos, key, got, value)
			}
		}
	}
	if n := providerArt.hits.Load(); n != 0 {
		t.Errorf("the provider's artwork was requested %d times: the album phase owns the cover", n)
	}
	if !bytes.Equal(readTestImage(t, pathAt(album, 1)), readTestImage(t, pathAt(album, 2))) {
		t.Error("the recognised track lost the album's cover")
	}
}

func TestResolveKeepsTheReleaseISRCOfAnAlbumTrack(t *testing.T) {
	album := newAlbum(t, "Persona", []string{"NEON - Le Ali"}, map[int]map[string][]string{
		1: {taglib.Title: {"NEON - Le Ali"}, taglib.Artist: {"Marracash"}},
	})
	release := tracklist("r1", "Official", "2019-10-31", "NEON - Le Ali")
	release.Tracks[0].ISRC = "ITUM72000289"
	provider := corpusProvider{name: "p", byTitle: map[string][]TrackInfo{
		"NEON - Le Ali": {{Title: "NEON - Le Ali", Artist: "Marracash", Album: "Persona", ISRC: "OTHER0000001"}},
	}}

	r := NewResolver([]Provider{provider}, logger.New(false), 0).WithReleaseFinder(&fakeFinder{byTitle: map[string][]Tracklist{"Persona": {release}}})
	if err := r.Resolve(t.Context(), []string{pathAt(album, 1)}, album); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := readTestTag(t, pathAt(album, 1), taglib.ISRC); got != "ITUM72000289" {
		t.Errorf("ISRC = %q, want the release's ITUM72000289", got)
	}
}

// iTunes allows one search every 3 s: asking it for an ISRC the release already gave only slows the album down.
func TestResolveSearchesNoFurtherForAnISRCTheReleaseGave(t *testing.T) {
	album := newAlbum(t, "Persona", []string{"NEON - Le Ali"}, map[int]map[string][]string{
		1: {taglib.Title: {"NEON - Le Ali"}, taglib.Artist: {"Marracash"}},
	})
	release := tracklist("r1", "Official", "2019-10-31", "NEON - Le Ali")
	release.Tracks[0].ISRC = "ITUM72000289"
	primary := corpusProvider{name: "primary", byTitle: map[string][]TrackInfo{
		"NEON - Le Ali": {{Title: "NEON - Le Ali", Artist: "Marracash", Album: "Persona", Genre: "Hip-Hop"}},
	}}
	filler := &mockProvider{name: "filler"}

	r := NewResolver([]Provider{primary, filler}, logger.New(false), 0).WithReleaseFinder(&fakeFinder{byTitle: map[string][]Tracklist{"Persona": {release}}})
	if err := r.Resolve(t.Context(), []string{pathAt(album, 1)}, album); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if filler.called {
		t.Error("the filler was searched although the primary match left only the release's ISRC missing")
	}
}

func TestAlbumPhaseStopsAtTheFirstMatchingEdition(t *testing.T) {
	album := newAlbum(t, "Persona (Deluxe)", []string{"NEON - Le Ali"}, map[int]map[string][]string{
		1: {taglib.Title: {"NEON - Le Ali"}, taglib.Artist: {"Marracash"}, taglib.Album: {"Persona"}},
	})
	release := tracklist("r1", "Official", "2019-10-31", "NEON - Le Ali")
	finder := &fakeFinder{byTitle: map[string][]Tracklist{"Persona (Deluxe)": {release}, "Persona": {release}}}

	NewResolver(nil, logger.New(false), 0).WithReleaseFinder(finder).resolveAlbum(t.Context(), album)

	if want := []string{"Persona (Deluxe)|Marracash|1"}; !slices.Equal(finder.queries, want) {
		t.Errorf("queries = %q, want %q: the first title already found the edition", finder.queries, want)
	}
}

// A file the album does not place gets the whole per-file phase rather than half-tagged album fields.
func TestResolveTagsAFileWithoutAPositionOnItsOwn(t *testing.T) {
	album := newAlbum(t, "Persona", []string{"NEON - Le Ali"}, map[int]map[string][]string{
		1: {taglib.Title: {"NEON - Le Ali"}, taglib.Artist: {"Marracash"}},
	})
	stray := testaudio.MP3(t, t.TempDir(), "stray.mp3", "0.1")
	tagTestFile(t, stray, "Stray", "Someone")
	provider := corpusProvider{name: "p", byTitle: map[string][]TrackInfo{
		"Stray": {{Title: "Stray", Artist: "Someone", Album: "Elsewhere", TrackNumber: 7}},
	}}

	r := NewResolver([]Provider{provider}, logger.New(false), 0)
	if err := r.Resolve(t.Context(), []string{pathAt(album, 1), stray}, album); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := readTestTag(t, stray, taglib.Album); got != "Elsewhere" {
		t.Errorf("album = %q, want the provider's Elsewhere", got)
	}
	if got := readTestTag(t, stray, taglib.TrackNumber); got != "7" {
		t.Errorf("track number = %q, want the provider's 7", got)
	}
}

// yt-dlp joins artists with a comma, so only the release can say this one is a single name.
func TestAlbumArtistComesWholeFromAReleaseThatCreditsIt(t *testing.T) {
	album := newAlbum(t, "Igor", []string{"Earfquake", "Puppet"}, map[int]map[string][]string{
		1: {taglib.Title: {"Earfquake"}, taglib.Artist: {"Tyler, The Creator"}},
		2: {taglib.Title: {"Puppet"}, taglib.Artist: {"Tyler, The Creator, Kanye West"}},
	})
	finder := &fakeFinder{byTitle: map[string][]Tracklist{"Igor": {{
		ID: "igor", Title: "Igor", Artist: "Tyler, The Creator", Status: "Official",
		Tracks: []ReleaseTrack{{TrackNumber: 1, Title: "Earfquake"}, {TrackNumber: 2, Title: "Puppet"}},
	}}}}

	r := NewResolver(nil, logger.New(false), 0).WithReleaseFinder(finder)
	r.resolveAlbum(t.Context(), album)

	for pos := range 2 {
		if got := readTestTag(t, pathAt(album, pos+1), taglib.AlbumArtist); got != "Tyler, The Creator" {
			t.Errorf("track %d: album artist = %q", pos+1, got)
		}
	}
}

func TestAlbumArtistStaysPrimaryWhenTheReleaseCreditsAnother(t *testing.T) {
	album := newAlbum(t, "Mix", []string{"One"}, map[int]map[string][]string{
		1: {taglib.Title: {"One"}, taglib.Artist: {"Drake, Future"}},
	})
	finder := &fakeFinder{byTitle: map[string][]Tracklist{"Mix": {{
		ID: "mix", Title: "Mix", Artist: "Various Artists", Status: "Official",
		Tracks: []ReleaseTrack{{TrackNumber: 1, Title: "One"}},
	}}}}

	r := NewResolver(nil, logger.New(false), 0).WithReleaseFinder(finder)
	r.resolveAlbum(t.Context(), album)

	if got := readTestTag(t, pathAt(album, 1), taglib.AlbumArtist); got != "Drake" {
		t.Errorf("album artist = %q, want Drake", got)
	}
}

func TestAlbumDownloadKeepsYouTubesTitle(t *testing.T) {
	album := newAlbum(t, "Legacy", []string{"Desperado (Live at The Forum, 1976)"}, map[int]map[string][]string{
		1: {taglib.Title: {"Desperado (Live at The Forum, 1976)"}, taglib.Artist: {"Eagles"}},
	})
	provider := corpusProvider{name: "first", byTitle: map[string][]TrackInfo{"Desperado": {{
		Title: "Desperado (Live at the Forum, Inglewood, 1976)", Artist: "Eagles", Album: "Other", Genre: "Rock",
	}}}}

	r := NewResolver([]Provider{provider}, logger.New(false), 0)
	if err := r.Resolve(t.Context(), []string{pathAt(album, 1)}, album); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := readTestTag(t, pathAt(album, 1), taglib.Title); got != "Desperado (Live at The Forum, 1976)" {
		t.Errorf("title = %q, want YouTube Music's", got)
	}
	if got := readTestTag(t, pathAt(album, 1), taglib.Genre); got != "Rock" {
		t.Errorf("genre = %q, want the provider's", got)
	}
}

// YouTube Music cuts titles at about 100 characters; the release has them whole.
func TestAlbumTakesTheTitleOfTheReleaseItAdopted(t *testing.T) {
	cut := "Take It to the Limit (Live at The Forum, Inglewood, CA, 10/20/1976 and Then Some More Words To Be L"
	whole := cut + "ong Enough)"
	album := newAlbum(t, "Legacy", []string{cut, "Other"}, map[int]map[string][]string{
		1: {taglib.Title: {cut}, taglib.Artist: {"Eagles"}},
		2: {taglib.Title: {"Other"}, taglib.Artist: {"Eagles"}},
	})
	finder := &fakeFinder{byTitle: map[string][]Tracklist{"Legacy": {{
		ID: "legacy", Title: "Legacy", Artist: "Eagles", Status: "Official",
		Tracks: []ReleaseTrack{{TrackNumber: 1, Title: whole}, {TrackNumber: 2, Title: "Something Else Entirely"}},
	}, {
		ID: "second", Title: "Legacy", Artist: "Eagles", Status: "Official",
		Tracks: []ReleaseTrack{{TrackNumber: 1, Title: whole}, {TrackNumber: 2, Title: "Other"}},
	}}}}

	r := NewResolver(nil, logger.New(false), 0).WithReleaseFinder(finder)
	r.resolveAlbum(t.Context(), album)

	if got := readTestTag(t, pathAt(album, 1), taglib.Title); got != whole {
		t.Errorf("title = %q, want the release's", got)
	}
	if got := readTestTag(t, pathAt(album, 2), taglib.Title); got != "Other" {
		t.Errorf("title = %q", got)
	}
}
