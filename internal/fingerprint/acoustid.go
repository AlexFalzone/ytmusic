package fingerprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/httpjson"
	"ytmusic/internal/metadata"
	"ytmusic/internal/throttle"
)

const defaultAcoustIDURL = "https://api.acoustid.org/v2/lookup"

// AcoustID's published limit.
const requestInterval = time.Second / 3

// Shared by every client: AcoustID counts requests per address, so concurrent runs split one rate.
var limiter = throttle.New(requestInterval)

var ErrKeyRejected = errors.New("AcoustID rejected the API key; acoustid_api_key must be an application's key, from https://acoustid.org/new-application")

// Not documented by AcoustID: observed answering a user key, with HTTP 400.
const codeInvalidKey = 4

type AcoustIDClient struct {
	apiKey  string
	baseURL string
	api     *httpjson.Client
}

// An empty baseURL means the real endpoint.
func NewAcoustIDClient(apiKey, baseURL string) *AcoustIDClient {
	if baseURL == "" {
		baseURL = defaultAcoustIDURL
	}
	return &AcoustIDClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		api: &httpjson.Client{
			HTTP:     &http.Client{Timeout: 10 * time.Second},
			Throttle: limiter,
		},
	}
}

type acoustidResponse struct {
	Status  string           `json:"status"`
	Error   *acoustidError   `json:"error"`
	Results []acoustidResult `json:"results"`
}

type acoustidError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type acoustidResult struct {
	ID         string              `json:"id"`
	Score      float64             `json:"score"`
	Recordings []acoustidRecording `json:"recordings"`
}

type acoustidRecording struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	Duration      float64                `json:"duration"` // seconds
	Artists       []acoustidName         `json:"artists"`
	ReleaseGroups []acoustidReleaseGroup `json:"releasegroups"`
}

type acoustidReleaseGroup struct {
	Title          string         `json:"title"`
	Artists        []acoustidName `json:"artists"`
	SecondaryTypes []string       `json:"secondarytypes"`
}

type acoustidName struct {
	Name string `json:"name"`
}

const minScore = 0.5

// Every recording of every result above minScore, best first: one fingerprint is often linked to several, some wrong.
func (c *AcoustIDClient) Lookup(ctx context.Context, fp Result) ([]metadata.Recording, error) {
	params := url.Values{}
	params.Set("client", c.apiKey)
	params.Set("duration", strconv.Itoa(fp.Duration))
	params.Set("fingerprint", fp.Fingerprint)
	params.Set("meta", "recordings releasegroups")

	var result acoustidResponse
	if err := c.api.PostForm(ctx, c.baseURL, nil, params, &result); err != nil {
		var se *httpjson.StatusError
		if errors.As(err, &se) && json.Unmarshal([]byte(se.Body), &result) == nil && rejectedKey(result) {
			return nil, fmt.Errorf("acoustid lookup: %w", ErrKeyRejected)
		}
		return nil, fmt.Errorf("acoustid lookup: %w", err)
	}
	if rejectedKey(result) {
		return nil, fmt.Errorf("acoustid lookup: %w", ErrKeyRejected)
	}
	if result.Status != "ok" {
		msg := "no message"
		if result.Error != nil {
			msg = result.Error.Message
		}
		return nil, fmt.Errorf("acoustid lookup: status %q: %s", result.Status, msg)
	}

	var recs []metadata.Recording
	seen := make(map[string]bool)
	for _, r := range result.Results {
		if r.Score < minScore {
			continue
		}
		for _, rec := range r.Recordings {
			if rec.ID == "" || seen[rec.ID] {
				continue
			}
			seen[rec.ID] = true
			recs = append(recs, toRecording(rec))
		}
	}
	return recs, nil
}

func toRecording(rec acoustidRecording) metadata.Recording {
	r := metadata.Recording{
		MBID:     rec.ID,
		Title:    rec.Title,
		Artist:   joinNames(rec.Artists),
		Duration: time.Duration(rec.Duration * float64(time.Second)),
	}
	for _, g := range rec.ReleaseGroups {
		r.Albums = append(r.Albums, metadata.RecordingAlbum{
			Title:     g.Title,
			Artist:    joinNames(g.Artists),
			Secondary: len(g.SecondaryTypes) > 0,
		})
	}
	return r
}

func joinNames(names []acoustidName) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n.Name
	}
	return strings.Join(parts, ", ")
}

func rejectedKey(r acoustidResponse) bool {
	return r.Error != nil && r.Error.Code == codeInvalidKey
}
