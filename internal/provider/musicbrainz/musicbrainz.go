package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/buildinfo"
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
	httpClient     *http.Client
	apiURL         string
	artworkBaseURL string
	throttle       *throttle.Throttle

	// The three phases of a run ask for the same recordings and releases:
	// phase A for the releases of each fingerprinted recording, the
	// fingerprint path for the same recordings' metadata, phase B for an
	// album phase A may already have fetched.
	recordings memo.Cache[string, recording]
	releases   memo.Cache[string, metadata.Tracklist]
}

// New creates a new MusicBrainz client.
func New() *Client {
	return &Client{
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		apiURL:         "https://musicbrainz.org/ws/2",
		artworkBaseURL: "https://coverartarchive.org/release",
		throttle:       throttle.New(requestInterval),
	}
}

// NewWithURL creates a client with custom API and artwork base URLs (used in tests).
func NewWithURL(apiURL, artworkBaseURL string) *Client {
	return &Client{
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		apiURL:         apiURL,
		artworkBaseURL: artworkBaseURL,
		throttle:       throttle.New(requestInterval),
	}
}

func (c *Client) Name() string { return "musicbrainz" }

// Search queries the MusicBrainz recording search API and returns matching tracks.
func (c *Client) Search(ctx context.Context, query metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	q := buildQuery(query)
	if q == "" {
		return nil, nil
	}

	reqURL := fmt.Sprintf("%s/recording?query=%s&fmt=json&limit=5", c.apiURL, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create musicbrainz request: %w", err)
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("musicbrainz search returned %d: %s", resp.StatusCode, body)
	}

	var searchResp searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode musicbrainz response: %w", err)
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
		reqURL := fmt.Sprintf("%s/recording/%s?inc=artists+releases+isrcs+artist-credits&fmt=json", c.apiURL, mbid)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return recording{}, fmt.Errorf("failed to create recording lookup request: %w", err)
		}
		req.Header.Set("User-Agent", buildinfo.UserAgent())
		req.Header.Set("Accept", "application/json")

		resp, err := c.doWithRetry(ctx, req)
		if err != nil {
			return recording{}, fmt.Errorf("recording lookup failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return recording{}, fmt.Errorf("recording lookup returned %d: %s", resp.StatusCode, body)
		}

		var rec recording
		if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
			return recording{}, fmt.Errorf("failed to decode musicbrainz recording: %w", err)
		}
		return rec, nil
	})
}

// doWithRetry waits for the throttle and executes the request, retrying once
// on 429/503 after the server's Retry-After.
func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	if err := c.throttle.Wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		resp.Body.Close()
		retryAfter := 2
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if parsed, err := strconv.Atoi(ra); err == nil {
				retryAfter = parsed
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(retryAfter) * time.Second):
		}

		if err := c.throttle.Wait(ctx); err != nil {
			return nil, err
		}
		retry := req.Clone(ctx)
		return c.httpClient.Do(retry)
	}

	return resp, nil
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
			info.Year = parseYear(rel.Date)
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

// releaseAlbumSim returns a rough similarity score between a release title and
// the preferred album name. Used only as a tiebreaker in pickBestRelease.
func releaseAlbumSim(releaseTitle, preferAlbum string) float64 {
	if preferAlbum == "" {
		return 0
	}
	a := strings.ToLower(strings.TrimSpace(releaseTitle))
	b := strings.ToLower(strings.TrimSpace(preferAlbum))
	if a == b {
		return 1.0
	}
	at := strings.Fields(a)
	bt := strings.Fields(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	set := make(map[string]bool, len(bt))
	for _, t := range bt {
		set[t] = true
	}
	matches := 0
	for _, t := range at {
		if set[t] {
			matches++
		}
	}
	maxLen := max(len(bt), len(at))
	return float64(matches) / float64(maxLen)
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

func parseYear(date string) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return 0
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

	reqURL := fmt.Sprintf("%s/release?query=%s&fmt=json&limit=5", c.apiURL, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create release search request: %w", err)
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("release search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("release search returned %d: %s", resp.StatusCode, body)
	}

	var result releaseListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode release search response: %w", err)
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
	reqURL := fmt.Sprintf("%s/release/%s?inc=recordings+artist-credits&fmt=json", c.apiURL, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return metadata.Tracklist{}, fmt.Errorf("failed to create release lookup request: %w", err)
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return metadata.Tracklist{}, fmt.Errorf("release lookup request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return metadata.Tracklist{}, fmt.Errorf("release lookup returned %d: %s", resp.StatusCode, body)
	}

	var result releaseLookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return metadata.Tracklist{}, fmt.Errorf("failed to decode release lookup response: %w", err)
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
