package downloader

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The fields --flat-playlist prints for each entry; a single video has only webpage_url and title.
const entryTemplate = "%(.{title,url,webpage_url,playlist_id,playlist_title,playlist_count,playlist_index})j"

type Entry struct {
	URL      string
	Title    string
	Position int // 1-based place in the playlist; 0 for a single video
}

type Playlist struct {
	ID      string
	Title   string
	Count   int
	Entries []Entry
}

// YouTube Music serves an album as an auto-generated "OLAK5uy_" playlist titled "Album - <name>", EPs and singles included.
func (p Playlist) Album() (string, bool) {
	if !strings.HasPrefix(p.ID, "OLAK5uy_") {
		return "", false
	}
	return strings.TrimPrefix(p.Title, "Album - "), true
}

// Each video once, in playlist order: a repeat would run two yt-dlp on the same files.
func (p Playlist) URLs() []string {
	seen := make(map[string]bool, len(p.Entries))
	var urls []string
	for _, e := range p.Entries {
		if !seen[e.URL] {
			seen[e.URL] = true
			urls = append(urls, e.URL)
		}
	}
	return urls
}

type flatEntry struct {
	URL           string `json:"url"`
	WebpageURL    string `json:"webpage_url"`
	Title         string `json:"title"`
	PlaylistID    string `json:"playlist_id"`
	PlaylistTitle string `json:"playlist_title"`
	PlaylistCount int    `json:"playlist_count"`
	PlaylistIndex int    `json:"playlist_index"`
}

func parsePlaylist(r io.Reader) (Playlist, error) {
	var pl Playlist
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e flatEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return Playlist{}, fmt.Errorf("parsing yt-dlp entry %q: %w", line, err)
		}
		url := cmp.Or(e.WebpageURL, e.URL)
		if url == "" {
			return Playlist{}, fmt.Errorf("yt-dlp entry without a URL: %q", line)
		}
		pl.ID, pl.Title, pl.Count = e.PlaylistID, e.PlaylistTitle, e.PlaylistCount
		pl.Entries = append(pl.Entries, Entry{
			// An album's entries point at music.youtube.com; yt-dlp picks other player clients there.
			URL:      strings.Replace(url, "https://music.youtube.com/", "https://www.youtube.com/", 1),
			Title:    e.Title,
			Position: e.PlaylistIndex,
		})
	}
	if err := scanner.Err(); err != nil {
		return Playlist{}, fmt.Errorf("reading yt-dlp output: %w", err)
	}
	return pl, nil
}
