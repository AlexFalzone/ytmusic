package fingerprint

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	"ytmusic/internal/memo"
	"ytmusic/internal/metadata"
)

type fpcalcGenerator interface {
	Generate(ctx context.Context, path string) (Result, error)
}

type acoustidLookup interface {
	Lookup(ctx context.Context, fp Result) (string, bool, error)
}

type defaultFpcalc struct{}

func (d *defaultFpcalc) Generate(ctx context.Context, path string) (Result, error) {
	return Generate(ctx, path)
}

type Fingerprinter struct {
	fpcalc     fpcalcGenerator
	acoustid   acoustidLookup
	mbidLookup func(ctx context.Context, mbid, preferAlbum string) (metadata.TrackInfo, error)

	// Phase A and the per-file phase look up the same files.
	recordings memo.Cache[string, recordingMatch]
}

// found=false is an answer too, and is remembered like one.
type recordingMatch struct {
	mbid  string
	found bool
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

			mbid, found, err := f.recordingID(ctx, path)
			if err != nil || !found {
				return
			}
			slots[i] = slot{match: metadata.FileMatch{Path: path, MBID: mbid}, ok: true}
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
