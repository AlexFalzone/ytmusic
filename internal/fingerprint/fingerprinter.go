package fingerprint

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"ytmusic/internal/memo"
	"ytmusic/internal/metadata"
)

type fpcalcGenerator interface {
	Generate(ctx context.Context, path string) (Result, error)
}

type acoustidLookup interface {
	Lookup(ctx context.Context, fp Result) ([]metadata.Recording, error)
}

var errFingerprintOff = fmt.Errorf("%w: %w", metadata.ErrFingerprintOff, ErrKeyRejected)

type defaultFpcalc struct{}

func (d *defaultFpcalc) Generate(ctx context.Context, path string) (Result, error) {
	return Generate(ctx, path)
}

type Fingerprinter struct {
	fpcalc     fpcalcGenerator
	acoustid   acoustidLookup
	mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)

	// Phase A and the per-file phase look up the same files; no recording is an answer too, and is remembered like one.
	recordings memo.Cache[string, []metadata.Recording]

	// A rejected key fails every lookup: the rest of the run skips fpcalc and AcoustID.
	rejected atomic.Bool
}

func New(acoustidClient *AcoustIDClient, mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)) *Fingerprinter {
	return &Fingerprinter{
		fpcalc:     &defaultFpcalc{},
		acoustid:   acoustidClient,
		mbidLookup: mbidLookup,
	}
}

func NewFingerprinter(fp fpcalcGenerator, ac acoustidLookup, mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)) *Fingerprinter {
	return &Fingerprinter{fpcalc: fp, acoustid: ac, mbidLookup: mbidLookup}
}

func (f *Fingerprinter) BatchLookupByFiles(ctx context.Context, paths []string) ([]metadata.FileMatch, error) {
	type slot struct {
		match metadata.FileMatch
		ok    bool
		panic error
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
			defer func() {
				if p := recover(); p != nil {
					slots[i].panic = fmt.Errorf("panic fingerprinting %s: %v\n%s", path, p, debug.Stack())
				}
			}()

			// Errors are dropped: the per-file phase looks each file up again and reports its error there.
			recs, err := f.Recordings(ctx, path)
			if err != nil || len(recs) == 0 {
				return
			}
			slots[i] = slot{match: metadata.FileMatch{Path: path, MBID: recs[0].MBID}, ok: true}
		}(i, path)
	}
	wg.Wait()

	var matches []metadata.FileMatch
	var panics []error
	for _, s := range slots {
		if s.ok {
			matches = append(matches, s.match)
		}
		if s.panic != nil {
			panics = append(panics, s.panic)
		}
	}
	return matches, errors.Join(panics...)
}

func (f *Fingerprinter) Recordings(ctx context.Context, path string) ([]metadata.Recording, error) {
	if f.rejected.Load() {
		return nil, errFingerprintOff
	}
	return f.recordings.Do(path, func() ([]metadata.Recording, error) {
		fp, err := f.fpcalc.Generate(ctx, path)
		if err != nil {
			return nil, err
		}
		recs, err := f.acoustid.Lookup(ctx, fp)
		if errors.Is(err, ErrKeyRejected) {
			f.rejected.Store(true)
			return nil, errFingerprintOff
		}
		return recs, err
	})
}

func (f *Fingerprinter) Track(ctx context.Context, mbid, album string) (metadata.TrackInfo, error) {
	return f.mbidLookup(ctx, mbid, album)
}
