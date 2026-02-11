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
