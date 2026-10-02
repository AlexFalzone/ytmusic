package fingerprint_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"ytmusic/internal/fingerprint"
	"ytmusic/internal/metadata"
)

type countingFpcalc struct {
	calls     atomic.Int32
	failFirst bool
}

func (c *countingFpcalc) Generate(_ context.Context, _ string) (fingerprint.Result, error) {
	if c.calls.Add(1) == 1 && c.failFirst {
		return fingerprint.Result{}, errors.New("fpcalc crashed")
	}
	return fingerprint.Result{Duration: 200, Fingerprint: "AQ"}, nil
}

type countingAcoustID struct{ calls atomic.Int32 }

func (c *countingAcoustID) Lookup(_ context.Context, _ fingerprint.Result) (string, bool, error) {
	c.calls.Add(1)
	return "mbid-1", true, nil
}

// Phase A fingerprints every file of an album group; the per-file phase then
// looks each of them up again.
func TestFingerprinter_FingerprintsEachFileOnce(t *testing.T) {
	fc, ac := &countingFpcalc{}, &countingAcoustID{}
	fp := fingerprint.NewFingerprinter(fc, ac, makeMBIDLookup(metadata.TrackInfo{Title: "Song"}, nil))

	if got, err := fp.BatchLookupByFiles(context.Background(), []string{"/a.mp3", "/b.mp3"}); err != nil || len(got) != 2 {
		t.Fatalf("batch matched %d files (err %v), want 2", len(got), err)
	}
	if _, found, err := fp.LookupByFile(context.Background(), "/a.mp3", ""); err != nil || !found {
		t.Fatalf("LookupByFile = %v, %v, want found", found, err)
	}

	if n := fc.calls.Load(); n != 2 {
		t.Errorf("fpcalc ran %d times, want 2", n)
	}
	if n := ac.calls.Load(); n != 2 {
		t.Errorf("AcoustID asked %d times, want 2", n)
	}
}

func TestFingerprinter_RetriesAfterAFailure(t *testing.T) {
	fc := &countingFpcalc{failFirst: true}
	fp := fingerprint.NewFingerprinter(fc, &countingAcoustID{}, makeMBIDLookup(metadata.TrackInfo{Title: "Song"}, nil))

	if _, found, _ := fp.LookupByFile(context.Background(), "/a.mp3", ""); found {
		t.Fatal("first lookup: want no match, fpcalc failed")
	}
	if _, found, err := fp.LookupByFile(context.Background(), "/a.mp3", ""); err != nil || !found {
		t.Fatalf("second lookup = %v, %v, want found", found, err)
	}
}
