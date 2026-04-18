package fingerprint

import (
	"context"

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
	mbidLookup func(ctx context.Context, mbid string) (metadata.TrackInfo, error)
}

// New creates a production Fingerprinter with real dependencies.
// mbidLookup is typically musicbrainzClient.LookupByMBID.
func New(acoustidClient *AcoustIDClient, mbidLookup func(ctx context.Context, mbid string) (metadata.TrackInfo, error)) *Fingerprinter {
	return &Fingerprinter{
		fpcalc:     &defaultFpcalc{},
		acoustid:   acoustidClient,
		mbidLookup: mbidLookup,
	}
}

// NewFingerprinter creates a Fingerprinter with injected dependencies (used in tests).
func NewFingerprinter(fp fpcalcGenerator, ac acoustidLookup, mbidLookup func(ctx context.Context, mbid string) (metadata.TrackInfo, error)) *Fingerprinter {
	return &Fingerprinter{fpcalc: fp, acoustid: ac, mbidLookup: mbidLookup}
}

// LookupByFile identifies the audio file at path via its acoustic fingerprint.
// Returns (zero, false, nil) when no match is found; errors are non-fatal (logged by caller).
func (f *Fingerprinter) LookupByFile(ctx context.Context, path string) (metadata.TrackInfo, bool, error) {
	fp, err := f.fpcalc.Generate(ctx, path)
	if err != nil {
		return metadata.TrackInfo{}, false, nil
	}

	mbid, found, err := f.acoustid.Lookup(ctx, fp)
	if err != nil || !found {
		return metadata.TrackInfo{}, false, nil
	}

	info, err := f.mbidLookup(ctx, mbid)
	if err != nil {
		return metadata.TrackInfo{}, false, nil
	}

	info.Confidence = 1.0
	return info, true, nil
}
