package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/httpjson"
	"ytmusic/internal/memo"
	"ytmusic/internal/metadata"
	"ytmusic/internal/throttle"
)

// requestInterval is MusicBrainz's rate limit: one request per second per
// client, enforced by throttling whoever exceeds it.
const requestInterval = time.Second

// Client is a MusicBrainz Web API client that implements metadata.Provider.
// Its throttle covers every request it sends, which is why a run must share
// one Client rather than create several.
type Client struct {
	api            *httpjson.Client
	apiURL         string
	artworkBaseURL string

	// The three phases of a run ask for the same recordings and releases:
	// phase A for the releases of each fingerprinted recording, the
	// fingerprint path for the same recordings' metadata, phase B for an
	// album phase A may already have fetched.
	recordings memo.Cache[string, recording]
	releases   memo.Cache[string, metadata.Tracklist]
}

// New creates a new MusicBrainz client.
func New() *Client {
	return NewWithURL("https://musicbrainz.org/ws/2", "https://coverartarchive.org/release")
}

// NewWithURL creates a client with custom API and artwork base URLs (used in tests).
func NewWithURL(apiURL, artworkBaseURL string) *Client {
	return &Client{
		api: &httpjson.Client{
			HTTP:     &http.Client{Timeout: 10 * time.Second},
			Throttle: throttle.New(requestInterval),
			Retry:    true,
		},
		apiURL:         apiURL,
		artworkBaseURL: artworkBaseURL,
	}
}

func (c *Client) Name() string { return "musicbrainz" }

// Search queries the MusicBrainz recording search API and returns matching tracks.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildQuery(query)
	if q == "" {
		return nil, nil
	}

	var searchResp searchResponse
	reqURL := fmt.Sprintf("%s/recording?query=%s&fmt=json&limit=5", c.apiURL, url.QueryEscape(q))
	if err := c.api.Get(ctx, reqURL, nil, &searchResp); err != nil {
		return nil, fmt.Errorf("musicbrainz search: %w", err)
	}

	return parseRecordings(searchResp.Recordings, query.Album, c.artworkBaseURL), nil
}

// LookupByMBID fetches a single recording by its MusicBrainz recording ID.
// preferAlbum, if non-empty, is used to break ties when the recording appears in multiple releases.
func (c *Client) LookupByMBID(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error) {
	rec, err := c.lookupRecording(ctx, mbid)
	if err != nil {
		return metadata.TrackInfo{}, err
	}

	results := parseRecordings([]recording{rec}, preferAlbum, c.artworkBaseURL)
	if len(results) == 0 {
		return metadata.TrackInfo{}, fmt.Errorf("no parseable data in musicbrainz recording %s", mbid)
	}
	return results[0], nil
}

// lookupRecording fetches a recording with everything both of its callers
// need, once per run.
func (c *Client) lookupRecording(ctx context.Context, mbid string) (recording, error) {
	return c.recordings.Do(mbid, func() (recording, error) {
		var rec recording
		reqURL := fmt.Sprintf("%s/recording/%s?inc=artists+releases+isrcs+artist-credits&fmt=json", c.apiURL, mbid)
		if err := c.api.Get(ctx, reqURL, nil, &rec); err != nil {
			return recording{}, fmt.Errorf("musicbrainz recording %s: %w", mbid, err)
		}
		return rec, nil
	})
}

func buildQuery(query metadata.SearchQuery) string {
	var parts []string
	if query.Title != "" {
		parts = append(parts, fmt.Sprintf("recording:%q", query.Title))
	}
	if query.Artist != "" {
		parts = append(parts, fmt.Sprintf("artist:%q", query.Artist))
	}
	if query.Album != "" {
		parts = append(parts, fmt.Sprintf("release:%q", query.Album))
	}
	return strings.Join(parts, " AND ")
}

// parseRecordings turns recordings into candidates. The artwork URL is the
// release's Cover Art Archive front image, not checked here: probing every
// candidate cost a request each, and the resolver downloads the one it keeps,
// falling back to another provider's artwork when that fails.
func parseRecordings(recordings []recording, preferAlbum, artworkBaseURL string) []metadata.TrackInfo {
	var results []metadata.TrackInfo
	for _, rec := range recordings {
		info := metadata.TrackInfo{
			Title:    rec.Title,
			Artist:   joinArtistCredits(rec.ArtistCredit),
			Duration: time.Duration(rec.Length) * time.Millisecond,
		}

		if len(rec.ISRCs) > 0 {
			info.ISRC = rec.ISRCs[0]
		}

		if len(rec.Releases) > 0 {
			rel := pickBestRelease(rec.Releases, preferAlbum)
			info.Album = rel.Title
			if len(rel.ArtistCredit) > 0 {
				info.AlbumArtist = rel.ArtistCredit[0].Artist.Name
			}
			info.Year = metadata.ParseYear(rel.Date)
			info.ReleaseDate = rel.Date

			info.ArtworkURL = fmt.Sprintf("%s/%s/front-500", artworkBaseURL, rel.ID)

			if len(rel.Media) > 0 && len(rel.Media[0].Track) > 0 {
				m := rel.Media[0]
				if n, err := strconv.Atoi(m.Track[0].Number); err == nil {
					info.TrackNumber = n
				} else if m.Track[0].Position > 0 {
					info.TrackNumber = m.Track[0].Position
				}
				if m.TrackCount > 0 {
					info.TotalTracks = m.TrackCount
				}
				if m.Position > 0 {
					info.DiscNumber = m.Position
				}
			}
		}

		results = append(results, info)
	}
	return results
}

func joinArtistCredits(credits []artistCredit) string {
	var parts []string
	for _, ac := range credits {
		parts = append(parts, ac.Artist.Name)
	}
	return strings.Join(parts, ", ")
}

// pickBestRelease selects the most appropriate release for tagging.
// Prefers: Official status, Album type, no secondary types (not Compilation).
// Among equal-scored releases, prefers releases whose title matches preferAlbum
// (to avoid landing on variants like "LP! OFFLINE" when the source is "LP!"),
// then the one with track position data, then the earliest date.
func pickBestRelease(releases []release, preferAlbum string) release {
	best := releases[0]
	bestScore := releaseScore(best)

	for _, rel := range releases[1:] {
		s := releaseScore(rel)
		relHasTrack := len(rel.Media) > 0 && len(rel.Media[0].Track) > 0
		bestHasTrack := len(best.Media) > 0 && len(best.Media[0].Track) > 0

		betterScore := s > bestScore
		sameScore := s == bestScore

		relAlbumSim := releaseAlbumSim(rel.Title, preferAlbum)
		bestAlbumSim := releaseAlbumSim(best.Title, preferAlbum)

		sameScoreBetterAlbum := sameScore && relAlbumSim > bestAlbumSim
		sameScoreSameAlbumWithTrack := sameScore && relAlbumSim == bestAlbumSim && relHasTrack && !bestHasTrack
		sameScoreSameAlbumEarlierDate := sameScore && relAlbumSim == bestAlbumSim && relHasTrack == bestHasTrack && rel.Date != "" && (best.Date == "" || rel.Date < best.Date)

		if betterScore || sameScoreBetterAlbum || sameScoreSameAlbumWithTrack || sameScoreSameAlbumEarlierDate {
			best = rel
			bestScore = s
		}
	}
	return best
}

// releaseAlbumSim is how close a release title is to the album the file
// declares, 0 when it declares none: two empty strings are perfectly alike,
// and would hand every tie to a release without a title.
func releaseAlbumSim(releaseTitle, preferAlbum string) float64 {
	if preferAlbum == "" {
		return 0
	}
	return metadata.Similarity(releaseTitle, preferAlbum)
}

func releaseScore(rel release) int {
	score := 0

	if rel.Status == "Official" {
		score += 4
	}

	if rel.ReleaseGroup.PrimaryType == "Album" {
		score += 2
	}

	if len(rel.ReleaseGroup.SecondaryTypes) == 0 {
		score += 1
	}

	return score
}

// MusicBrainz API response types

type searchResponse struct {
	Recordings []recording `json:"recordings"`
}

type recording struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Length       int            `json:"length"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	Releases     []release      `json:"releases"`
	ISRCs        []string       `json:"isrcs"`
}

type artistCredit struct {
	Artist artistInfo `json:"artist"`
}

type artistInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type release struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	Date         string         `json:"date"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	ReleaseGroup releaseGroup   `json:"release-group"`
	Media        []media        `json:"media"`
}

type releaseGroup struct {
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
}

type media struct {
	Position   int     `json:"position"`    // disc number (1-indexed)
	TrackCount int     `json:"track-count"` // total tracks on this disc
	Track      []track `json:"track"`
}

type track struct {
	Number   string `json:"number"`   // display number (may be non-numeric, e.g. "A1")
	Position int    `json:"position"` // numeric position, used when Number is non-numeric
}

// Release search / lookup types

type releaseListResponse struct {
	Releases []release `json:"releases"`
}

type releaseLookupResponse struct {
	ID           string                `json:"id"`
	Title        string                `json:"title"`
	ArtistCredit []artistCredit        `json:"artist-credit"`
	Media        []releaseLookupMedium `json:"media"`
}

type releaseLookupMedium struct {
	Position int                  `json:"position"`
	Tracks   []releaseLookupTrack `json:"tracks"`
}

type releaseLookupTrack struct {
	Number    string           `json:"number"`
	Position  int              `json:"position"`
	Title     string           `json:"title"`
	Recording releaseLookupRec `json:"recording"`
}

type releaseLookupRec struct {
	ID string `json:"id"`
}

// searchRelease queries MusicBrainz for releases matching album + artist.
func (c *Client) searchRelease(ctx context.Context, album, artist string) ([]release, error) {
	q := fmt.Sprintf("release:%q", album)
	if artist != "" {
		q += fmt.Sprintf(" AND artist:%q", artist)
	}

	var result releaseListResponse
	reqURL := fmt.Sprintf("%s/release?query=%s&fmt=json&limit=5", c.apiURL, url.QueryEscape(q))
	if err := c.api.Get(ctx, reqURL, nil, &result); err != nil {
		return nil, fmt.Errorf("musicbrainz release search: %w", err)
	}
	return result.Releases, nil
}

// lookupRelease returns the full tracklist for a release by its MusicBrainz
// ID, fetched once per run.
func (c *Client) lookupRelease(ctx context.Context, releaseID string) (metadata.Tracklist, error) {
	return c.releases.Do(releaseID, func() (metadata.Tracklist, error) {
		return c.fetchRelease(ctx, releaseID)
	})
}

func (c *Client) fetchRelease(ctx context.Context, releaseID string) (metadata.Tracklist, error) {
	var result releaseLookupResponse
	reqURL := fmt.Sprintf("%s/release/%s?inc=recordings+artist-credits&fmt=json", c.apiURL, releaseID)
	if err := c.api.Get(ctx, reqURL, nil, &result); err != nil {
		return metadata.Tracklist{}, fmt.Errorf("musicbrainz release %s: %w", releaseID, err)
	}

	tl := metadata.Tracklist{
		ID:    result.ID,
		Title: result.Title,
	}
	if len(result.ArtistCredit) > 0 {
		tl.Artist = result.ArtistCredit[0].Artist.Name
	}

	for _, m := range result.Media {
		for _, t := range m.Tracks {
			trackNum := t.Position
			if n, err := strconv.Atoi(t.Number); err == nil {
				trackNum = n
			}
			tl.Tracks = append(tl.Tracks, metadata.ReleaseTrack{
				TrackNumber: trackNum,
				DiscNumber:  m.Position,
				Title:       t.Title,
				MBID:        t.Recording.ID,
			})
		}
	}
	return tl, nil
}

// ReleaseIDsForRecording returns all release IDs that contain the given recording MBID.
// Implements metadata.ReleaseResolver.
func (c *Client) ReleaseIDsForRecording(ctx context.Context, mbid string) ([]string, error) {
	rec, err := c.lookupRecording(ctx, mbid)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(rec.Releases))
	for _, rel := range rec.Releases {
		ids = append(ids, rel.ID)
	}
	return ids, nil
}

// LookupTracklist fetches the complete tracklist for a release by its MusicBrainz ID.
// Implements metadata.ReleaseResolver.
func (c *Client) LookupTracklist(ctx context.Context, releaseID string) (metadata.Tracklist, error) {
	return c.lookupRelease(ctx, releaseID)
}

// ResolveAlbum implements metadata.AlbumResolver: searches for the best matching
// release and returns its complete tracklist.
func (c *Client) ResolveAlbum(ctx context.Context, album, artist string) (metadata.Tracklist, bool, error) {
	candidates, err := c.searchRelease(ctx, album, artist)
	if err != nil {
		return metadata.Tracklist{}, false, fmt.Errorf("release search failed: %w", err)
	}
	if len(candidates) == 0 {
		return metadata.Tracklist{}, false, nil
	}

	best := pickBestRelease(candidates, album)
	tl, err := c.lookupRelease(ctx, best.ID)
	if err != nil {
		return metadata.Tracklist{}, false, fmt.Errorf("release lookup failed: %w", err)
	}
	return tl, true, nil
}
