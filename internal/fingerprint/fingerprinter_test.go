package fingerprint_test

import (
	"context"
	"errors"
	"testing"

	"ytmusic/internal/fingerprint"
	"ytmusic/internal/metadata"
)

func makeMBIDLookup(info metadata.TrackInfo, err error) func(context.Context, string, string) (metadata.TrackInfo, error) {
	return func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error) {
		return info, err
	}
}

type stubAcoustID struct {
	mbid  string
	found bool
	err   error
}

func (s *stubAcoustID) Lookup(_ context.Context, _ fingerprint.Result) (string, bool, error) {
	return s.mbid, s.found, s.err
}

type stubFpcalc struct {
	result fingerprint.Result
	err    error
}

func (s *stubFpcalc) Generate(_ context.Context, _ string) (fingerprint.Result, error) {
	return s.result, s.err
}

func TestFingerprinter_LookupByFile_Success(t *testing.T) {
	want := metadata.TrackInfo{Title: "Song", Artist: "Band", Album: "Record", Confidence: 1.0}
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(want, nil),
	)

	got, found, err := fp.LookupByFile(context.Background(), "/fake/path.mp3", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if got.Title != want.Title || got.Artist != want.Artist {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", got.Confidence)
	}
}

func TestFingerprinter_LookupByFile_FpcalcFails(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{err: errors.New("fpcalc not found")},
		&stubAcoustID{},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	_, found, err := fp.LookupByFile(context.Background(), "/fake/path.mp3", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when fpcalc fails")
	}
}

func TestFingerprinter_LookupByFile_AcoustIDNotFound(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{found: false},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	_, found, err := fp.LookupByFile(context.Background(), "/fake/path.mp3", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when AcoustID finds nothing")
	}
}

func TestFingerprinter_LookupByFile_MBLookupFails(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, errors.New("mb down")),
	)

	_, found, err := fp.LookupByFile(context.Background(), "/fake/path.mp3", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when MB lookup fails")
	}
}

func TestBatchLookupByFiles_CollectsMBIDs(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	paths := []string{"/a.mp3", "/b.mp3", "/c.mp3"}
	matches := fp.BatchLookupByFiles(context.Background(), paths)

	if len(matches) != 3 {
		t.Fatalf("got %d matches, want 3", len(matches))
	}
	for _, m := range matches {
		if m.MBID != "mbid-1" {
			t.Errorf("MBID = %q, want mbid-1", m.MBID)
		}
	}
}

func TestBatchLookupByFiles_FpcalcFails_Skipped(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{err: errors.New("fpcalc not found")},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	matches := fp.BatchLookupByFiles(context.Background(), []string{"/a.mp3", "/b.mp3"})
	if len(matches) != 0 {
		t.Errorf("expected 0 matches when fpcalc fails, got %d", len(matches))
	}
}

func TestBatchLookupByFiles_AcoustIDNotFound_Skipped(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{found: false},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	matches := fp.BatchLookupByFiles(context.Background(), []string{"/a.mp3"})
	if len(matches) != 0 {
		t.Errorf("expected 0 matches when AcoustID finds nothing, got %d", len(matches))
	}
}

func TestBatchLookupByFiles_EmptyPaths(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	matches := fp.BatchLookupByFiles(context.Background(), nil)
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty paths, got %d", len(matches))
	}
}
