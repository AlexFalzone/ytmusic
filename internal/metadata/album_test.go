package metadata

import (
	"context"
	"errors"
	"testing"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

func TestMatchTrackByTitle_FindsBestMatch(t *testing.T) {
	tracks := []ReleaseTrack{
		{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
		{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
		{TrackNumber: 3, DiscNumber: 1, Title: "NEMO!"},
	}
	got, score := matchTrackByTitle("TRUST!", tracks)
	if got.TrackNumber != 1 {
		t.Errorf("TrackNumber = %d, want 1", got.TrackNumber)
	}
	if score < 0.9 {
		t.Errorf("score = %.2f, want >= 0.9", score)
	}
}

func TestMatchTrackByTitle_LowScoreForUnrelated(t *testing.T) {
	tracks := []ReleaseTrack{
		{TrackNumber: 1, Title: "TRUST!"},
		{TrackNumber: 2, Title: "DIRTY!"},
	}
	_, score := matchTrackByTitle("COMPLETELY DIFFERENT SONG", tracks)
	if score > 0.3 {
		t.Errorf("score = %.2f, want <= 0.3 for unrelated title", score)
	}
}

func TestMatchTrackByTitle_NormalizesBeforeComparing(t *testing.T) {
	tracks := []ReleaseTrack{
		{TrackNumber: 5, Title: "ARE U HAPPY?"},
	}
	got, score := matchTrackByTitle("ARE U HAPPY?", tracks)
	if got.TrackNumber != 5 {
		t.Errorf("TrackNumber = %d, want 5", got.TrackNumber)
	}
	if score < 0.9 {
		t.Errorf("score = %.2f, want >= 0.9 for exact title", score)
	}
}

func TestGroupByAlbum_GroupsSameAlbumTogether(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)
	p3 := newTestMP3(t)

	for _, p := range []string{p1, p2} {
		writeTestTags(t, p, map[string][]string{taglib.Album: {"LP!"}})
	}
	writeTestTags(t, p3, map[string][]string{taglib.Album: {"Veteran"}})

	groups := newEvalResolver().groupByAlbum([]string{p1, p2, p3})

	if len(groups["LP!"]) != 2 {
		t.Errorf("LP! group size = %d, want 2", len(groups["LP!"]))
	}
	if len(groups["Veteran"]) != 1 {
		t.Errorf("Veteran group size = %d, want 1", len(groups["Veteran"]))
	}
}

func TestGroupByAlbum_FilesWithNoAlbumGetOwnGroup(t *testing.T) {
	p := newTestMP3(t)

	groups := newEvalResolver().groupByAlbum([]string{p})

	total := 0
	for _, files := range groups {
		total += len(files)
	}
	if total != 1 {
		t.Errorf("expected 1 file total across groups, got %d", total)
	}
}

type mockBatchFingerprinter struct {
	matches []FileMatch
	err     error
}

func (m *mockBatchFingerprinter) BatchLookupByFiles(_ context.Context, _ []string) ([]FileMatch, error) {
	return m.matches, m.err
}

type mockReleaseResolver struct {
	releaseIDs map[string][]string  // mbid → release IDs
	tracklists map[string]Tracklist // releaseID → tracklist
}

func (m *mockReleaseResolver) ReleaseIDsForRecording(_ context.Context, mbid string) ([]string, error) {
	return m.releaseIDs[mbid], nil
}

func (m *mockReleaseResolver) LookupTracklist(_ context.Context, releaseID string) (Tracklist, error) {
	return m.tracklists[releaseID], nil
}

type mockAlbumResolver struct {
	tracklist Tracklist
	found     bool
	err       error
}

func (m *mockAlbumResolver) ResolveAlbum(_ context.Context, _, _ string) (Tracklist, bool, error) {
	return m.tracklist, m.found, m.err
}

func TestFindDominantRelease_ReturnsMajority(t *testing.T) {
	// rel-lp holds 2 of 3 recordings: 2 >= 1.5.
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{
			"mbid-1": {"rel-lp"},
			"mbid-2": {"rel-lp"},
			"mbid-3": {"rel-offline"},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithReleaseResolver(rr)

	dominantID, found := r.findDominantRelease(context.Background(), []string{"mbid-1", "mbid-2", "mbid-3"})
	if !found {
		t.Fatal("expected dominant release to be found")
	}
	if dominantID != "rel-lp" {
		t.Errorf("dominantID = %q, want rel-lp", dominantID)
	}
}

func TestFindDominantRelease_NoQuorum_NotFound(t *testing.T) {
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{
			"mbid-1": {"rel-a"},
			"mbid-2": {"rel-b"},
			"mbid-3": {"rel-c"},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithReleaseResolver(rr)

	_, found := r.findDominantRelease(context.Background(), []string{"mbid-1", "mbid-2", "mbid-3"})
	if found {
		t.Fatal("expected no dominant release when no quorum")
	}
}

func TestFindDominantRelease_EmptyMBIDs(t *testing.T) {
	rr := &mockReleaseResolver{}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithReleaseResolver(rr)

	_, found := r.findDominantRelease(context.Background(), nil)
	if found {
		t.Fatal("expected no dominant release for empty MBIDs")
	}
}

func TestResolveGroupByFingerprint_WritesPositionalTags(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	writeTestTags(t, p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})
	writeTestTags(t, p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})

	bf := &mockBatchFingerprinter{
		matches: []FileMatch{
			{Path: p1, MBID: "mbid-1"},
			{Path: p2, MBID: "mbid-2"},
		},
	}
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{
			"mbid-1": {"rel-lp"},
			"mbid-2": {"rel-lp"},
		},
		tracklists: map[string]Tracklist{
			"rel-lp": {
				ID:    "rel-lp",
				Title: "LP!",
				Tracks: []ReleaseTrack{
					{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!", MBID: "mbid-1"},
					{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!", MBID: "mbid-2"},
				},
			},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithBatchFingerprinter(bf)
	r.WithReleaseResolver(rr)

	resolved := r.resolveGroupByFingerprint(context.Background(), []string{p1, p2})

	if len(resolved) != 2 {
		t.Fatalf("resolved %d files, want 2", len(resolved))
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := FirstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want 1", got)
	}
	if got := FirstTag(tags1, taglib.DiscNumber); got != "1" {
		t.Errorf("p1 DiscNumber = %q, want 1", got)
	}

	tags2, _ := taglib.ReadTags(p2)
	if got := FirstTag(tags2, taglib.TrackNumber); got != "2" {
		t.Errorf("p2 TrackNumber = %q, want 2", got)
	}
}

func TestResolveGroupByFingerprint_TooFewFingerprinted_DoesNothing(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)
	p3 := newTestMP3(t)

	bf := &mockBatchFingerprinter{
		matches: []FileMatch{{Path: p1, MBID: "mbid-1"}},
	}
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{"mbid-1": {"rel-lp"}},
		tracklists: map[string]Tracklist{
			"rel-lp": {Tracks: []ReleaseTrack{{TrackNumber: 1, Title: "TRUST!"}}},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	r.WithBatchFingerprinter(bf)
	r.WithReleaseResolver(rr)

	resolved := r.resolveGroupByFingerprint(context.Background(), []string{p1, p2, p3})

	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved, got %d (coverage too low)", len(resolved))
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := FirstTag(tags1, taglib.TrackNumber); got != "" {
		t.Errorf("TrackNumber should not be written when coverage < 50%%, got %q", got)
	}
}

func TestResolveGroup_WritesPositionalTags(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	writeTestTags(t, p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})
	writeTestTags(t, p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})

	ar := &mockAlbumResolver{
		found: true,
		tracklist: Tracklist{
			ID:    "abc",
			Title: "LP!",
			Tracks: []ReleaseTrack{
				{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
				{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
			},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	if err := r.resolveGroup(context.Background(), "LP!", []string{p1, p2}, ar); err != nil {
		t.Fatalf("resolveGroup: %v", err)
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := FirstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want %q", got, "1")
	}
	if got := FirstTag(tags1, taglib.DiscNumber); got != "1" {
		t.Errorf("p1 DiscNumber = %q, want %q", got, "1")
	}

	tags2, _ := taglib.ReadTags(p2)
	if got := FirstTag(tags2, taglib.TrackNumber); got != "2" {
		t.Errorf("p2 TrackNumber = %q, want %q", got, "2")
	}
}

func TestResolveGroup_NotFound_DoesNothing(t *testing.T) {
	p := newTestMP3(t)
	writeTestTags(t, p, map[string][]string{
		taglib.Title: {"TRUST!"},
		taglib.Album: {"LP!"},
	})

	ar := &mockAlbumResolver{found: false}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	if err := r.resolveGroup(context.Background(), "LP!", []string{p}, ar); err != nil {
		t.Fatalf("resolveGroup: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := FirstTag(tags, taglib.TrackNumber); got != "" {
		t.Errorf("TrackNumber should not be written when not found, got %q", got)
	}
}

func TestResolveGroup_LowTitleMatch_DoesNotWriteTags(t *testing.T) {
	p := newTestMP3(t)
	writeTestTags(t, p, map[string][]string{
		taglib.Title: {"COMPLETELY DIFFERENT TRACK"},
		taglib.Album: {"LP!"},
	})

	ar := &mockAlbumResolver{
		found: true,
		tracklist: Tracklist{
			Tracks: []ReleaseTrack{
				{TrackNumber: 1, Title: "TRUST!"},
				{TrackNumber: 2, Title: "DIRTY!"},
			},
		},
	}

	log := logger.New(false)
	r := NewResolver(nil, log, 0)
	if err := r.resolveGroup(context.Background(), "LP!", []string{p}, ar); err != nil {
		t.Fatalf("resolveGroup: %v", err)
	}

	tags, _ := taglib.ReadTags(p)
	if got := FirstTag(tags, taglib.TrackNumber); got != "" {
		t.Errorf("TrackNumber should not be written for low match, got %q", got)
	}
}

func TestResolve_AlbumFirstPhaseWritesPositionalTags(t *testing.T) {
	p1 := newTestMP3(t)
	p2 := newTestMP3(t)

	writeTestTags(t, p1, map[string][]string{
		taglib.Title:  {"TRUST!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})
	writeTestTags(t, p2, map[string][]string{
		taglib.Title:  {"DIRTY!"},
		taglib.Artist: {"JPEGMAFIA"},
		taglib.Album:  {"LP!"},
	})

	ar := &mockAlbumResolver{
		found: true,
		tracklist: Tracklist{
			Tracks: []ReleaseTrack{
				{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
				{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
			},
		},
	}

	// No results, so phase 3 cannot touch the positional tags.
	mock := &mockProvider{name: "empty", results: nil}

	log := logger.New(false)
	r := NewResolver([]Provider{mock}, log, 0.9)
	r.WithAlbumResolver(ar)

	if err := r.Resolve(context.Background(), []string{p1, p2}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	tags1, _ := taglib.ReadTags(p1)
	if got := FirstTag(tags1, taglib.TrackNumber); got != "1" {
		t.Errorf("p1 TrackNumber = %q, want %q", got, "1")
	}

	tags2, _ := taglib.ReadTags(p2)
	if got := FirstTag(tags2, taglib.TrackNumber); got != "2" {
		t.Errorf("p2 TrackNumber = %q, want %q", got, "2")
	}
}

func TestResolveGroupByFingerprint_KeepsMatchesBesideAFailure(t *testing.T) {
	p1, p2 := newTestMP3(t), newTestMP3(t)
	writeTestTags(t, p1, map[string][]string{taglib.Title: {"TRUST!"}, taglib.Album: {"LP!"}})
	writeTestTags(t, p2, map[string][]string{taglib.Title: {"DIRTY!"}, taglib.Album: {"LP!"}})

	bf := &mockBatchFingerprinter{
		matches: []FileMatch{{Path: p1, MBID: "mbid-1"}},
		err:     errors.New("panic fingerprinting p2"),
	}
	rr := &mockReleaseResolver{
		releaseIDs: map[string][]string{"mbid-1": {"rel-lp"}},
		tracklists: map[string]Tracklist{"rel-lp": {Tracks: []ReleaseTrack{
			{TrackNumber: 1, DiscNumber: 1, Title: "TRUST!"},
			{TrackNumber: 2, DiscNumber: 1, Title: "DIRTY!"},
		}}},
	}
	r := NewResolver(nil, logger.New(false), 0).WithBatchFingerprinter(bf).WithReleaseResolver(rr)

	if got := r.resolveGroupByFingerprint(context.Background(), []string{p1, p2}); len(got) != 2 {
		t.Errorf("resolved %d files, want 2: coverage is 50%%, the tracklist covers both", len(got))
	}
}
