package metadata

import (
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
