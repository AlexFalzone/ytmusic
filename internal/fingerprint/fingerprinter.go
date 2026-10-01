package fingerprint

import (
	"context"
	"sync"

	"ytmusic/internal/memo"
	"ytmusic/internal/metadata"
)

// fpcalcGenerator abstracts the fpcalc CLI (mockable in tests).
type fpcalcGenerator interface {
	Generate(ctx context.Context, path string) (Result, error)
}

// acoustidLookup abstracts the AcoustID client (mockable in tests).
type acoustidLookup interface {
	Lookup(ctx context.Context, fp Result) (string, bool, error)
}

// defaultFpcalc wraps the package-level Generate function.
type defaultFpcalc struct{}

func (d *defaultFpcalc) Generate(ctx context.Context, path string) (Result, error) {
	return Generate(ctx, path)
}

// Fingerprinter implements metadata.Fingerprinter using Chromaprint + AcoustID + MusicBrainz.
type Fingerprinter struct {
	fpcalc     fpcalcGenerator
	acoustid   acoustidLookup
	mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)

	// The batch phase fingerprints whole album groups and the per-file phase
	// looks the same files up again: fpcalc and AcoustID run once per file.
	recordings memo.Cache[string, recordingMatch]
}

// recordingMatch is what AcoustID made of one file. Not finding a recording
// is an answer too, and is remembered like one.
type recordingMatch struct {
	mbid  string
	found bool
}

// New creates a production Fingerprinter with real dependencies.
// mbidLookup is typically musicbrainzClient.LookupByMBID.
func New(acoustidClient *AcoustIDClient, mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)) *Fingerprinter {
	return &Fingerprinter{
		fpcalc:     &defaultFpcalc{},
		acoustid:   acoustidClient,
		mbidLookup: mbidLookup,
	}
}

// NewFingerprinter creates a Fingerprinter with injected dependencies (used in tests).
func NewFingerprinter(fp fpcalcGenerator, ac acoustidLookup, mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)) *Fingerprinter {
	return &Fingerprinter{fpcalc: fp, acoustid: ac, mbidLookup: mbidLookup}
}

// BatchLookupByFiles fingerprints all paths in parallel (max 4 concurrent) and
// returns FileMatch entries only for files whose AcoustID lookup returned a recording MBID.
// The mbidLookup step is intentionally skipped here; callers use the MBID directly.
func (f *Fingerprinter) BatchLookupByFiles(ctx context.Context, paths []string) []metadata.FileMatch {
	type slot struct {
		match metadata.FileMatch
		ok    bool
	}

	slots := make([]slot, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mbid, found, err := f.recordingID(ctx, path)
			if err != nil || !found {
				return
			}
			slots[i] = slot{match: metadata.FileMatch{Path: path, MBID: mbid}, ok: true}
		}(i, path)
	}
	wg.Wait()

	var matches []metadata.FileMatch
	for _, s := range slots {
		if s.ok {
			matches = append(matches, s.match)
		}
	}
	return matches
}

// LookupByFile identifies the audio file at path via its acoustic fingerprint.
// preferAlbum is passed to MusicBrainz to break ties when a recording appears in multiple releases.
// Returns (zero, false, nil) when no match is found; errors are non-fatal (logged by caller).
func (f *Fingerprinter) LookupByFile(ctx context.Context, path, preferAlbum string) (metadata.TrackInfo, bool, error) {
	mbid, found, err := f.recordingID(ctx, path)
	if err != nil || !found {
		return metadata.TrackInfo{}, false, nil
	}

	info, err := f.mbidLookup(ctx, mbid, preferAlbum)
	if err != nil {
		return metadata.TrackInfo{}, false, nil
	}

	info.Confidence = 1.0
	return info, true, nil
}

// recordingID fingerprints the file and asks AcoustID for its recording, once
// per file for the run.
func (f *Fingerprinter) recordingID(ctx context.Context, path string) (string, bool, error) {
	m, err := f.recordings.Do(path, func() (recordingMatch, error) {
		fp, err := f.fpcalc.Generate(ctx, path)
		if err != nil {
			return recordingMatch{}, err
		}
		mbid, found, err := f.acoustid.Lookup(ctx, fp)
		if err != nil {
			return recordingMatch{}, err
		}
		return recordingMatch{mbid: mbid, found: found}, nil
	})
	return m.mbid, m.found, err
}
