package metadata

import (
	"context"
	"sync"
	"testing"
	"time"

	"ytmusic/internal/logger"
)

// Answers Recordings by path and Track by MBID, and remembers what Track was asked.
type fingerprintStub struct {
	byPath map[string][]Recording
	tracks map[string]TrackInfo

	mu    sync.Mutex
	asked []string // "mbid@album"
}

func (s *fingerprintStub) Recordings(_ context.Context, path string) ([]Recording, error) {
	return s.byPath[path], nil
}

func (s *fingerprintStub) Track(_ context.Context, mbid, album string) (TrackInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, mbid+"@"+album)
	return s.tracks[mbid], nil
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }

// The cases measured on real AcoustID answers, where the first recording linked to the audio was the wrong one.
func TestFingerprintMatchTakesOnlyARecordingTheFileConfirms(t *testing.T) {
	takeCare := RecordingAlbum{Title: "Take Care", Artist: "Drake"}
	tests := []struct {
		name                 string
		title, artist, album string
		length               int
		recs                 []Recording
		want                 string // "mbid@album" asked of Track, "" for no match
	}{
		{
			name:  "a shorter song linked first",
			title: "Look What You've Done", artist: "Drake", length: 301,
			recs: []Recording{
				{MBID: "hyfr", Title: "HYFR (Hell Ya Fuckin’ Right)", Artist: "Drake, Lil Wayne", Duration: seconds(206), Albums: []RecordingAlbum{takeCare}},
				{MBID: "lwyd", Title: "Look What You’ve Done", Artist: "Drake", Duration: seconds(301), Albums: []RecordingAlbum{takeCare}},
			},
			want: "lwyd@Take Care",
		},
		{
			name:  "a remix and another artist's song of the same title",
			title: "Katy Perry - The One That Got Away (Official Music Video)", artist: "Katy Perry", length: 289,
			recs: []Recording{
				{MBID: "r3hab", Title: "The One That Got Away (R3hab remix)", Artist: "Katy Perry", Albums: []RecordingAlbum{{Title: "The One That Got Away (remixes)"}}},
				{MBID: "waits", Title: "The One That Got Away", Artist: "Tom Waits", Duration: seconds(291), Albums: []RecordingAlbum{{Title: "Small Change", Artist: "Tom Waits"}}},
				{MBID: "katy", Title: "The One That Got Away", Artist: "Katy Perry", Duration: seconds(227), Albums: []RecordingAlbum{{Title: "Teenage Dream", Artist: "Katy Perry"}}},
			},
			want: "katy@Teenage Dream",
		},
		{
			name:  "only a remix of the song",
			title: "Shakira, Burna Boy - Dai Dai (Official Video)", artist: "Shakira, Burna Boy, FIFA", length: 240,
			recs: []Recording{{MBID: "spinall", Title: "Dai dai (SPINALL remix)", Artist: "Shakira, Burna Boy", Duration: seconds(235),
				Albums: []RecordingAlbum{{Title: "Dai dai (remixes)"}}}},
		},
		{
			name:  "another song sharing a word of the title",
			title: `KATSEYE (캣츠아이) "Animal" Official MV`, artist: "HYBE LABELS", length: 191,
			recs: []Recording{{MBID: "vortex", Title: "Animal Instinct", Artist: "Zak Vortex", Duration: seconds(190),
				Albums: []RecordingAlbum{{Title: "Miami Squeeze", Artist: "Zak Vortex"}}}},
		},
		{
			name:  "a label's upload naming the artist in the title",
			title: "LISA - SaWaDiKa (Official Music Video)", artist: "LLOUD Official", length: 180,
			recs: []Recording{{MBID: "lisa", Title: "SaWaDiKa", Artist: "LISA", Duration: seconds(175),
				Albums: []RecordingAlbum{{Title: "SaWaDiKa", Artist: "LISA"}}}},
			want: "lisa@SaWaDiKa",
		},
		{
			name:  "a spelling the title mostly shares",
			title: "HYFR (Hell Ya Fucking Right)", artist: "Drake, Lil Wayne", length: 206,
			recs: []Recording{{MBID: "hyfr", Title: "HYFR (Hell Ya Fuckin’ Right)", Artist: "Drake, Lil Wayne", Duration: seconds(206),
				Albums: []RecordingAlbum{takeCare}}},
			want: "hyfr@Take Care",
		},
		{
			name:  "the recording on the file's album over a compilation's copy",
			title: "Take Care", artist: "Drake, Rihanna", album: "Take Care", length: 277,
			recs: []Recording{
				{MBID: "comp", Title: "Take Care", Artist: "Drake", Duration: seconds(280),
					Albums: []RecordingAlbum{{Title: "Heart & Soul", Artist: "Various Artists", Secondary: true}}},
				{MBID: "album", Title: "Take Care", Artist: "Drake, Rihanna", Duration: seconds(277),
					Albums: []RecordingAlbum{{Title: "Absolute Music 70", Artist: "Various Artists", Secondary: true}, takeCare}},
			},
			want: "album@Take Care",
		},
		{
			name:  "without an album, the release group that is no compilation",
			title: "Song", artist: "Band", length: 200,
			recs: []Recording{{MBID: "song", Title: "Song", Artist: "Band", Duration: seconds(200),
				Albums: []RecordingAlbum{{Title: "Hits 2012", Artist: "Various Artists", Secondary: true}, {Title: "Record", Artist: "Band"}}}},
			want: "song@Record",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &fingerprintStub{byPath: map[string][]Recording{"/f.mp3": tt.recs}}
			r := NewResolver(nil, logger.New(false), 0).WithFingerprinter(stub)
			query, version := NormalizeQuery(tt.title, tt.artist)
			query.Album = tt.album
			src := source{query: query, version: version, duration: seconds(tt.length)}

			_, found := r.fingerprintMatch(context.Background(), "/f.mp3", src, tt.title, tt.artist)
			if found != (tt.want != "") {
				t.Errorf("found = %v, want %v", found, tt.want != "")
			}
			var asked string
			if len(stub.asked) > 0 {
				asked = stub.asked[0]
			}
			if asked != tt.want {
				t.Errorf("looked up %q, want %q", asked, tt.want)
			}
		})
	}
}
