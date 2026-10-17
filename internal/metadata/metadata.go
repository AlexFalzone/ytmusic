package metadata

import (
	"context"
	"strconv"
	"time"
)

type TrackInfo struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	TrackNumber int
	TotalTracks int
	DiscNumber  int
	Year        int
	ReleaseDate string
	Genre       string
	ISRC        string
	ArtworkURL  string
	Duration    time.Duration
	Confidence  float64
}

func ParseYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

type SearchQuery struct {
	Title  string
	Artist string
	Album  string
}

type Provider interface {
	Name() string
	Search(ctx context.Context, query SearchQuery) ([]TrackInfo, error)
}

type ReleaseTrack struct {
	TrackNumber int
	DiscNumber  int
	Title       string
	MBID        string
	ISRC        string
}

type Tracklist struct {
	ID         string
	Title      string
	Artist     string
	Status     string // "Official", "Bootleg", …
	Date       string
	ArtworkURL string // "" when the release has no front cover
	Tracks     []ReleaseTrack
}

// An album as its source presents it: its title, order and track count win over any provider.
type Album struct {
	Title  string
	Tracks []string       // titles in album order, "" where unknown; the length is the track count
	Files  map[string]int // path → 1-based position in Tracks
}

type AlbumResolver interface {
	ResolveAlbum(ctx context.Context, album, artist string) (Tracklist, bool, error)
}

type Fingerprinter interface {
	LookupByFile(ctx context.Context, path, preferAlbum string) (TrackInfo, bool, error)
}

type FileMatch struct {
	Path string
	MBID string
}

// The error carries recovered panics, each of which failed only its own file.
type BatchFingerprinter interface {
	BatchLookupByFiles(ctx context.Context, paths []string) ([]FileMatch, error)
}

// Releases with exactly tracks tracks, each with its tracklist.
type ReleaseFinder interface {
	FindReleases(ctx context.Context, title, artist string, tracks int) ([]Tracklist, error)
}

type ReleaseResolver interface {
	ReleaseIDsForRecording(ctx context.Context, mbid string) ([]string, error)
	LookupTracklist(ctx context.Context, releaseID string) (Tracklist, error)
}
