package fingerprint_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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
	calls atomic.Int32
}

func (s *stubAcoustID) Lookup(_ context.Context, _ fingerprint.Result) (string, bool, error) {
	s.calls.Add(1)
	return s.mbid, s.found, s.err
}

type stubFpcalc struct {
	result fingerprint.Result
	err    error
	calls  atomic.Int32
}

func (s *stubFpcalc) Generate(_ context.Context, _ string) (fingerprint.Result, error) {
	s.calls.Add(1)
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

func TestFingerprinter_LookupByFile_ReportsErrors(t *testing.T) {
	stubErr := errors.New("fpcalc: exit status 2")
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{err: stubErr},
		&stubAcoustID{},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)
	_, found, err := fp.LookupByFile(context.Background(), "/fake/path.mp3", "")
	if !errors.Is(err, stubErr) || found {
		t.Errorf("fpcalc failing: found=%v err=%v, want not found and the fpcalc error", found, err)
	}

	mbErr := errors.New("mb down")
	fp = fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, mbErr),
	)
	_, found, err = fp.LookupByFile(context.Background(), "/fake/path.mp3", "")
	if !errors.Is(err, mbErr) || found {
		t.Errorf("MusicBrainz failing: found=%v err=%v, want not found and the lookup error", found, err)
	}
}

// Every file would be refused the same way: one refusal is enough to stop asking.
func TestFingerprinter_StopsAfterTheKeyIsRejected(t *testing.T) {
	fpcalc := &stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}}
	acoustid := &stubAcoustID{err: fmt.Errorf("acoustid lookup: %w", fingerprint.ErrKeyRejected)}
	fp := fingerprint.NewFingerprinter(fpcalc, acoustid, makeMBIDLookup(metadata.TrackInfo{}, nil))

	for _, path := range []string{"/a.mp3", "/b.mp3"} {
		_, _, err := fp.LookupByFile(context.Background(), path, "")
		if !errors.Is(err, metadata.ErrFingerprintOff) || !errors.Is(err, fingerprint.ErrKeyRejected) {
			t.Errorf("%s: err = %v, want fingerprinting off because of the rejected key", path, err)
		}
	}
	if got := acoustid.calls.Load(); got != 1 {
		t.Errorf("%d AcoustID lookups, want 1", got)
	}
	if got := fpcalc.calls.Load(); got != 1 {
		t.Errorf("%d fpcalc runs, want 1", got)
	}
}

func TestBatchLookupByFiles_CollectsMBIDs(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	paths := []string{"/a.mp3", "/b.mp3", "/c.mp3"}
	matches, err := fp.BatchLookupByFiles(context.Background(), paths)
	if err != nil {
		t.Fatalf("BatchLookupByFiles: %v", err)
	}

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

	matches, err := fp.BatchLookupByFiles(context.Background(), []string{"/a.mp3", "/b.mp3"})
	if err != nil {
		t.Fatalf("BatchLookupByFiles: %v", err)
	}
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

	matches, err := fp.BatchLookupByFiles(context.Background(), []string{"/a.mp3"})
	if err != nil {
		t.Fatalf("BatchLookupByFiles: %v", err)
	}
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

	matches, err := fp.BatchLookupByFiles(context.Background(), nil)
	if err != nil {
		t.Fatalf("BatchLookupByFiles: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty paths, got %d", len(matches))
	}
}

type panickingFpcalc struct{ on string }

func (p panickingFpcalc) Generate(_ context.Context, path string) (fingerprint.Result, error) {
	if path == p.on {
		panic("fpcalc bug")
	}
	return fingerprint.Result{Duration: 200, Fingerprint: "AQx"}, nil
}

func TestBatchLookupByFiles_PanicFailsOnlyItsFile(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		panickingFpcalc{on: "/b.mp3"},
		&stubAcoustID{mbid: "mbid-1", found: true},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	matches, err := fp.BatchLookupByFiles(context.Background(), []string{"/a.mp3", "/b.mp3"})
	if len(matches) != 1 || matches[0].Path != "/a.mp3" {
		t.Errorf("matches = %+v, want only /a.mp3", matches)
	}
	if err == nil || !strings.Contains(err.Error(), "/b.mp3") {
		t.Errorf("err = %v, want the panic on /b.mp3", err)
	}
}
