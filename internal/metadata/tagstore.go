package metadata

import (
	"strconv"

	"ytmusic/internal/memo"

	"go.senan.xyz/taglib"
)

// tagStore reads each file's tags from disk once and serves them from memory
// after that. The album-first phases write the positional tags that the
// per-file phase reads back to protect them, so every write drops the file's
// copy and the next read goes back to disk. A file is only ever handled by one
// goroutine at a time, which is what makes reading around a write safe.
type tagStore struct {
	tags memo.Cache[string, map[string][]string]
}

// read returns path's tags. The map is shared with later readers: callers
// must not modify it.
func (s *tagStore) read(path string) (map[string][]string, error) {
	return s.tags.Do(path, func() (map[string][]string, error) {
		return taglib.ReadTags(path)
	})
}

// write sets the given tags on path and leaves the others as they are. The
// copy is dropped even when the write fails: the file may be half written.
func (s *tagStore) write(path string, tags map[string][]string) error {
	if len(tags) == 0 {
		return nil
	}
	defer s.tags.Forget(path)
	return taglib.WriteTags(path, tags, 0)
}

// writePositional writes the track and disc numbers, skipping zeros.
func (s *tagStore) writePositional(path string, trackNum, discNum int) error {
	tags := make(map[string][]string)
	if trackNum > 0 {
		tags[taglib.TrackNumber] = []string{strconv.Itoa(trackNum)}
	}
	if discNum > 0 {
		tags[taglib.DiscNumber] = []string{strconv.Itoa(discNum)}
	}
	return s.write(path, tags)
}
