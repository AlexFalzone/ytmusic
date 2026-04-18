package fingerprint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultAcoustIDURL = "https://api.acoustid.org/v2/lookup"

// AcoustIDClient queries the AcoustID API to resolve a fingerprint to a MusicBrainz recording ID.
type AcoustIDClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewAcoustIDClient creates a new client. baseURL overrides the default endpoint (used in tests).
func NewAcoustIDClient(apiKey, baseURL string) *AcoustIDClient {
	if baseURL == "" {
		baseURL = defaultAcoustIDURL
	}
	return &AcoustIDClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type acoustidResponse struct {
	Status  string           `json:"status"`
	Results []acoustidResult `json:"results"`
}

type acoustidResult struct {
	ID         string             `json:"id"`
	Score      float64            `json:"score"`
	Recordings []acoustidRecording `json:"recordings"`
}

type acoustidRecording struct {
	ID string `json:"id"`
}

// Lookup submits a fingerprint to AcoustID and returns the first MusicBrainz recording ID found.
// Returns (mbid, true, nil) on success, ("", false, nil) when no match is found.
func (c *AcoustIDClient) Lookup(ctx context.Context, fp Result) (string, bool, error) {
	params := url.Values{}
	params.Set("client", c.apiKey)
	params.Set("duration", strconv.Itoa(fp.Duration))
	params.Set("fingerprint", fp.Fingerprint)
	params.Set("meta", "recordingids")

	reqURL := c.baseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("failed to build acoustid request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("acoustid request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("acoustid returned %d", resp.StatusCode)
	}

	var result acoustidResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, fmt.Errorf("failed to decode acoustid response: %w", err)
	}

	for _, r := range result.Results {
		if r.Score < 0.5 {
			continue
		}
		for _, rec := range r.Recordings {
			if rec.ID != "" {
				return rec.ID, true, nil
			}
		}
	}

	return "", false, nil
}
