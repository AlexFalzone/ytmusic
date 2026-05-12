package metadata

import (
	"context"
	"time"
)

// TrackInfo contains metadata for a single audio track.
type TrackInfo struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	TrackNumber int
	TotalTracks int
	DiscNumber  int
	Year        int
	ReleaseDate string // full date "2020-03-20" when available
	Genre       string
	ISRC        string
	ArtworkURL  string
	Duration    time.Duration
	Confidence  float64 // 0.0-1.0, how confident we are in the match
}

// SearchQuery represents a cleaned-up query for searching metadata providers.
type SearchQuery struct {
	Title  string
	Artist string
	Album  string
}

// Provider is the interface that metadata providers must implement.
type Provider interface {
	Name() string
	Search(ctx context.Context, query SearchQuery) ([]TrackInfo, error)
}

// ReleaseTrack is a single track within a release tracklist.
type ReleaseTrack struct {
	TrackNumber int
	DiscNumber  int
	Title       string
	MBID        string
}

// Tracklist is the complete track listing of a music release.
type Tracklist struct {
	ID     string
	Title  string
	Artist string
	Tracks []ReleaseTrack
}

// AlbumResolver looks up a release's complete tracklist by album name and artist.
type AlbumResolver interface {
	ResolveAlbum(ctx context.Context, album, artist string) (Tracklist, bool, error)
}

// Fingerprinter identifies an audio file by its acoustic fingerprint and returns
// the best matching TrackInfo. preferAlbum hints which release to prefer when a
// recording appears in multiple albums. Returns (zero, false, nil) when no match is found.
type Fingerprinter interface {
	LookupByFile(ctx context.Context, path, preferAlbum string) (TrackInfo, bool, error)
}

// FileMatch holds an audio file path and its AcoustID recording MBID.
type FileMatch struct {
	Path string
	MBID string
}

// BatchFingerprinter fingerprints multiple files in parallel and returns
// only the files for which an AcoustID recording MBID was found.
type BatchFingerprinter interface {
	BatchLookupByFiles(ctx context.Context, paths []string) []FileMatch
}

// ReleaseResolver looks up which releases contain a recording and fetches
// a full tracklist by release ID.
type ReleaseResolver interface {
	ReleaseIDsForRecording(ctx context.Context, mbid string) ([]string, error)
	LookupTracklist(ctx context.Context, releaseID string) (Tracklist, error)
}
