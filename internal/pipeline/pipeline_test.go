package pipeline

import (
	"strings"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/downloader"
)

func TestCheckToolsNeedsFpcalcOnlyWithAcoustID(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := CheckTools(config.Config{})
	if err == nil {
		t.Fatal("want an error with nothing on PATH, got none")
	}
	for _, tool := range []string{"yt-dlp", "ffmpeg", "ffprobe"} {
		if !strings.Contains(err.Error(), tool) {
			t.Errorf("error should name %s, got: %v", tool, err)
		}
	}
	if strings.Contains(err.Error(), "fpcalc") {
		t.Errorf("fpcalc is required without an AcoustID key: %v", err)
	}

	err = CheckTools(config.Config{AcoustIDAPIKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "fpcalc") {
		t.Errorf("want fpcalc required with an AcoustID key, got: %v", err)
	}
}

func TestCheckImportToolsNeedsNoDownloader(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if err := CheckImportTools(config.Config{}); err != nil {
		t.Errorf("import without AcoustID needs no external tool, got: %v", err)
	}

	err := CheckImportTools(config.Config{AcoustIDAPIKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "fpcalc") {
		t.Fatalf("want fpcalc required with an AcoustID key, got: %v", err)
	}
	if strings.Contains(err.Error(), "yt-dlp") {
		t.Errorf("import never downloads, yet yt-dlp is required: %v", err)
	}
}

func TestCheckToolsDryRunNeedsOnlyYtdlp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := CheckTools(config.Config{DryRun: true, AcoustIDAPIKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "yt-dlp") {
		t.Fatalf("want yt-dlp required, got: %v", err)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe", "fpcalc"} {
		if strings.Contains(err.Error(), tool) {
			t.Errorf("dry run requires %s, which it never runs: %v", tool, err)
		}
	}
}

// YouTube can hide an unavailable track from the list while still counting it.
func TestAlbumForKeepsYouTubesTrackCount(t *testing.T) {
	pl := downloader.Playlist{ID: "OLAK5uy_x", Title: "Album - Record", Count: 3, Entries: []downloader.Entry{
		{URL: "u1", Title: "One", Position: 1},
		{URL: "u2", Title: "Two", Position: 2},
	}}

	album := albumFor(pl, map[string][]string{"u2": {"/tmp/a/Two.mp3"}}, map[string]string{"/tmp/a/Two.mp3": "/tmp/merged/Two.mp3"})

	if len(album.Tracks) != 3 || album.Tracks[0] != "One" || album.Tracks[2] != "" {
		t.Errorf("tracks = %q, want [One Two \"\"]: three tracks, the third unknown", album.Tracks)
	}
	if album.Files["/tmp/merged/Two.mp3"] != 2 || len(album.Files) != 1 {
		t.Errorf("files = %v, want the merged Two.mp3 at 2", album.Files)
	}
}
