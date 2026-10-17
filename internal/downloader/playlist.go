package downloader

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The fields --flat-playlist prints for each entry; a single video has only id and title.
const entryTemplate = "%(.{id,title,playlist_id,playlist_title,playlist_count,playlist_index})j"

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

type flatEntry struct {
	ID            string `json:"id"`
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
		pl.ID, pl.Title, pl.Count = e.PlaylistID, e.PlaylistTitle, e.PlaylistCount
		pl.Entries = append(pl.Entries, Entry{
			URL:      "https://www.youtube.com/watch?v=" + e.ID,
			Title:    e.Title,
			Position: e.PlaylistIndex,
		})
	}
	if err := scanner.Err(); err != nil {
		return Playlist{}, fmt.Errorf("reading yt-dlp output: %w", err)
	}
	return pl, nil
}
