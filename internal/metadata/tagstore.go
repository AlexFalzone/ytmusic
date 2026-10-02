package metadata

import (
	"strconv"

	"ytmusic/internal/memo"

	"go.senan.xyz/taglib"
)

// Writes drop the cached copy, so the per-file phase reads the album phases' track numbers. Safe only because a file has one goroutine at a time.
type tagStore struct {
	tags memo.Cache[string, map[string][]string]
}

// The map is shared with later readers: do not modify it.
func (s *tagStore) read(path string) (map[string][]string, error) {
	return s.tags.Do(path, func() (map[string][]string, error) {
		return taglib.ReadTags(path)
	})
}

// The copy is dropped even on failure: the file may be half written.
func (s *tagStore) write(path string, tags map[string][]string) error {
	if len(tags) == 0 {
		return nil
	}
	defer s.tags.Forget(path)
	return taglib.WriteTags(path, tags, 0)
}

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
