package fingerprint_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	recs  []metadata.Recording
	err   error
	calls atomic.Int32
}

func (s *stubAcoustID) Lookup(_ context.Context, _ fingerprint.Result) ([]metadata.Recording, error) {
	s.calls.Add(1)
	return s.recs, s.err
}

var oneRecording = []metadata.Recording{{MBID: "mbid-1", Title: "Song", Artist: "Band"}}

type stubFpcalc struct {
	result fingerprint.Result
	err    error
	calls  atomic.Int32
}

func (s *stubFpcalc) Generate(_ context.Context, _ string) (fingerprint.Result, error) {
	s.calls.Add(1)
	return s.result, s.err
}

func TestFingerprinter_Recordings_ReturnsWhatAcoustIDMatched(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{recs: oneRecording},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	got, err := fp.Recordings(context.Background(), "/fake/path.mp3")
	if err != nil {
		t.Fatalf("Recordings: %v", err)
	}
	if !reflect.DeepEqual(got, oneRecording) {
		t.Errorf("got %+v, want %+v", got, oneRecording)
	}
}

func TestFingerprinter_Recordings_NoneIsAnAnswer(t *testing.T) {
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}},
		&stubAcoustID{},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)

	got, err := fp.Recordings(context.Background(), "/fake/path.mp3")
	if err != nil || len(got) != 0 {
		t.Errorf("Recordings = %+v, %v; want none and no error", got, err)
	}
}

func TestFingerprinter_Track(t *testing.T) {
	want := metadata.TrackInfo{Title: "Song", Artist: "Band", Album: "Record"}
	var gotMBID, gotAlbum string
	fp := fingerprint.NewFingerprinter(&stubFpcalc{}, &stubAcoustID{},
		func(_ context.Context, mbid, album string) (metadata.TrackInfo, error) {
			gotMBID, gotAlbum = mbid, album
			return want, nil
		})

	got, err := fp.Track(context.Background(), "mbid-1", "Record")
	if err != nil || got != want {
		t.Errorf("Track = %+v, %v; want %+v", got, err, want)
	}
	if gotMBID != "mbid-1" || gotAlbum != "Record" {
		t.Errorf("looked up %q on %q, want mbid-1 on Record", gotMBID, gotAlbum)
	}
}

func TestFingerprinter_LookupByFile_ReportsErrors(t *testing.T) {
	stubErr := errors.New("fpcalc: exit status 2")
	fp := fingerprint.NewFingerprinter(
		&stubFpcalc{err: stubErr},
		&stubAcoustID{},
		makeMBIDLookup(metadata.TrackInfo{}, nil),
	)
	if _, err := fp.Recordings(context.Background(), "/fake/path.mp3"); !errors.Is(err, stubErr) {
		t.Errorf("fpcalc failing: err = %v, want the fpcalc error", err)
	}

	mbErr := errors.New("mb down")
	fp = fingerprint.NewFingerprinter(&stubFpcalc{}, &stubAcoustID{}, makeMBIDLookup(metadata.TrackInfo{}, mbErr))
	if _, err := fp.Track(context.Background(), "mbid-1", ""); !errors.Is(err, mbErr) {
		t.Errorf("MusicBrainz failing: err = %v, want the lookup error", err)
	}
}

// Every file would be refused the same way: one refusal is enough to stop asking.
func TestFingerprinter_StopsAfterTheKeyIsRejected(t *testing.T) {
	fpcalc := &stubFpcalc{result: fingerprint.Result{Duration: 200, Fingerprint: "AQx"}}
	acoustid := &stubAcoustID{err: fmt.Errorf("acoustid lookup: %w", fingerprint.ErrKeyRejected)}
	fp := fingerprint.NewFingerprinter(fpcalc, acoustid, makeMBIDLookup(metadata.TrackInfo{}, nil))

	for _, path := range []string{"/a.mp3", "/b.mp3"} {
		_, err := fp.Recordings(context.Background(), path)
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
		&stubAcoustID{recs: oneRecording},
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
		&stubAcoustID{recs: oneRecording},
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
		&stubAcoustID{},
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
		&stubAcoustID{recs: oneRecording},
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
		&stubAcoustID{recs: oneRecording},
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
