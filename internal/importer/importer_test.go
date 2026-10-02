package importer

import (
	"context"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/metadata"
	"ytmusic/internal/testaudio"

	"go.senan.xyz/taglib"
)

func albumFiles(t *testing.T, dir, album string, titles ...string) []string {
	t.Helper()
	var paths []string
	for _, title := range titles {
		p := testaudio.MP3(t, dir, title+".mp3", "0.1")
		tags := map[string][]string{taglib.Title: {title}, taglib.Artist: {"Band"}, taglib.Album: {album}}
		if err := taglib.WriteTags(p, tags, 0); err != nil {
			t.Fatalf("tag %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	return paths
}

func tag(t *testing.T, path, key string) string {
	t.Helper()
	tags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return metadata.FirstTag(tags, key)
}

type fingerprints struct{ mbids map[string]string }

func (f fingerprints) LookupByFile(context.Context, string, string) (metadata.TrackInfo, bool, error) {
	return metadata.TrackInfo{}, false, nil
}

func (f fingerprints) BatchLookupByFiles(_ context.Context, paths []string) ([]metadata.FileMatch, error) {
	var out []metadata.FileMatch
	for _, p := range paths {
		if id, ok := f.mbids[p]; ok {
			out = append(out, metadata.FileMatch{Path: p, MBID: id})
		}
	}
	return out, nil
}

type releases struct{ tracklist metadata.Tracklist }

func (r releases) ReleaseIDsForRecording(context.Context, string) ([]string, error) {
	return []string{"rel-x"}, nil
}

func (r releases) LookupTracklist(context.Context, string) (metadata.Tracklist, error) {
	return r.tracklist, nil
}

type albums struct{ tracklist metadata.Tracklist }

func (a albums) ResolveAlbum(_ context.Context, album, _ string) (metadata.Tracklist, bool, error) {
	return a.tracklist, album == "Y", nil
}

type everything struct{}

func (everything) Name() string { return "everything" }

func (everything) Search(_ context.Context, q metadata.SearchQuery) ([]metadata.TrackInfo, error) {
	return []metadata.TrackInfo{{
		Title: q.Title, Artist: q.Artist, Album: q.Album, Year: 2001, TrackNumber: 99, DiscNumber: 9,
	}}, nil
}

// Album X is placed by phase A, album Y by phase B, and phase 3 fills the rest without touching their track numbers.
func TestImportRunsTheThreePhasesTogether(t *testing.T) {
	dir := t.TempDir()
	x := albumFiles(t, dir, "X", "Alpha", "Beta")
	y := albumFiles(t, dir, "Y", "Gamma", "Delta")

	c := components{
		providers:     []metadata.Provider{everything{}},
		fingerprinter: fingerprints{mbids: map[string]string{x[0]: "m-alpha", x[1]: "m-beta"}},
		releaseResolver: releases{metadata.Tracklist{Tracks: []metadata.ReleaseTrack{
			{TrackNumber: 1, DiscNumber: 1, Title: "Alpha"},
			{TrackNumber: 2, DiscNumber: 1, Title: "Beta"},
		}}},
		albumResolver: albums{metadata.Tracklist{Tracks: []metadata.ReleaseTrack{
			{TrackNumber: 5, DiscNumber: 1, Title: "Gamma"},
			{TrackNumber: 6, DiscNumber: 1, Title: "Delta"},
		}}},
	}
	imp := newImporter(config.Config{MetadataWorkers: 2}, logger.New(false), c)
	if err := imp.Import(context.Background(), dir); err != nil {
		t.Fatalf("Import: %v", err)
	}

	want := map[string]string{x[0]: "1", x[1]: "2", y[0]: "5", y[1]: "6"}
	for path, track := range want {
		if got := tag(t, path, taglib.TrackNumber); got != track {
			t.Errorf("%s: TrackNumber = %q, want %q", path, got, track)
		}
		if got := tag(t, path, taglib.Date); got != "2001" {
			t.Errorf("%s: Date = %q, want 2001 from the per-file phase", path, got)
		}
	}
}

func TestImportWithNothingConfiguredLeavesFilesAlone(t *testing.T) {
	dir := t.TempDir()
	p := albumFiles(t, dir, "X", "Alpha")[0]

	imp := newImporter(config.Config{MetadataWorkers: 1}, logger.New(false), components{})
	if err := imp.Import(context.Background(), dir); err != nil {
		t.Fatalf("Import = %v, want nil: nothing to resolve with is not an error", err)
	}
	if got := tag(t, p, taglib.Date); got != "" {
		t.Errorf("Date = %q, want the file untouched", got)
	}
}

func TestImportFailsWithoutAudioFiles(t *testing.T) {
	imp := newImporter(config.Config{MetadataWorkers: 1}, logger.New(false),
		components{providers: []metadata.Provider{everything{}}})
	if err := imp.Import(context.Background(), t.TempDir()); err == nil {
		t.Error("Import = nil, want an error for a directory without audio")
	}
}
