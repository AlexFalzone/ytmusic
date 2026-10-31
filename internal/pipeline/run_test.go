package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/lyrics"
	"ytmusic/internal/metadata"
	"ytmusic/pkg/utils"

	"go.senan.xyz/taglib"
)

// listing is what --flat-playlist prints. Every other call copies fixture where -o points and reports it; name is the shell expression for the file's title.
// Id "dead" leaves an empty file there and fails, like a conversion that ran out of disk.
func writeFakeYtdlp(t *testing.T, listing, fixture, name string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
for a in "$@"; do last="$a"; done
case " $* " in
*" --flat-playlist "*)
	cat <<'LISTING'
` + listing + `
LISTING
	exit 0;;
esac
echo "$last" >> "` + calls + `"
next=""
for a in "$@"; do
	case "$next" in
	o) tmpl="$a";;
	list) list="$a";;
	esac
	next=""
	[ "$a" = "-o" ] && next=o
	[ "$a" = "after_move:filepath" ] && next=list
done
id="${last##*=}"
name=` + name + `
out=$(printf '%s' "$tmpl" | sed -e "s/%(id)s/$id/" -e "s/%(title)\.200B/$name/" -e 's/%(ext)s/mp3/')
mkdir -p "$(dirname "$out")"
if [ "$id" = "dead" ]; then : > "$out"; echo "ERROR: unable to write data: [Errno 122] Disk quota exceeded" >&2; exit 1; fi
/bin/cp -f "` + fixture + `" "$out"
printf '%s\n' "$out" > "$list"
`
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// Lists "song" and "dead" in playlist id.
func fakeYtdlp(t *testing.T, fixture, id, title string) {
	t.Helper()
	entry := `{"webpage_url": "https://www.youtube.com/watch?v=%s", "title": "%s", "playlist_id": "` + id + `", "playlist_title": "` + title + `", "playlist_count": 2, "playlist_index": %d}`
	writeFakeYtdlp(t, fmt.Sprintf(entry, "song", "Song", 1)+"\n"+fmt.Sprintf(entry, "dead", "Dead", 2), fixture, `"$id"`)
}

// Renames every album, so the test can tell files were imported before being moved.
type retagger struct {
	dirs   []string
	albums []*metadata.Album
	err    error
}

func (r *retagger) Import(_ context.Context, dir string, album *metadata.Album) error {
	r.dirs = append(r.dirs, dir)
	r.albums = append(r.albums, album)
	files, err := utils.FindAudioFiles(dir)
	if err != nil {
		return err
	}
	for _, p := range files {
		if err := taglib.WriteTags(p, map[string][]string{taglib.Album: {"Imported"}}, 0); err != nil {
			return err
		}
	}
	return r.err
}

type runResult struct {
	out       string
	tmp       string
	extracted int
	warnings  []string
}

func runFake(t *testing.T, imp dirImporter) runResult {
	t.Helper()
	return runFakePlaylist(t, imp, "PLx", "Mix")
}

func runFakePlaylist(t *testing.T, imp dirImporter, id, title string) runResult {
	t.Helper()
	cfg, hooks, res := setupFakeRun(t, id, title)
	if err := run(t.Context(), cfg, logger.New(false), res.tmp, hooks, imp, songLyrics()); err != nil {
		t.Fatalf("run: %v", err)
	}
	return *res
}

// The fake yt-dlp, a config that writes into temp dirs, and hooks that record into the result.
func setupFakeRun(t *testing.T, id, title string) (config.Config, Hooks, *runResult) {
	t.Helper()
	fixture := trackFile(t, t.TempDir(), "fixture.mp3", map[string][]string{
		taglib.Title: {"Song"}, taglib.Artist: {"Artist"}, taglib.Album: {"Album"},
	})
	fakeYtdlp(t, fixture, id, title)

	res := &runResult{out: t.TempDir(), tmp: t.TempDir()}
	cfg := config.DefaultConfig()
	cfg.PlaylistURL = "https://www.youtube.com/playlist?list=x"
	cfg.OutputDir = res.out
	cfg.ParallelJobs = 1
	hooks := Hooks{
		OnURLsExtracted: func(total int) { res.extracted = total },
		OnWarning:       func(msg string) { res.warnings = append(res.warnings, msg) },
	}
	return cfg, hooks, res
}

func songLyrics() *fakeLyrics {
	return &fakeLyrics{byTitle: map[string]lyrics.Result{"Song": {Synced: "[00:01.00]la"}}}
}

func libraryFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := utils.FindAudioFiles(dir)
	if err != nil {
		t.Fatalf("listing the library: %v", err)
	}
	return files
}

func hasWarning(warnings []string, part string) bool {
	for _, w := range warnings {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

func TestRunDownloadsImportsAndFilesTheTracks(t *testing.T) {
	imp := &retagger{}
	res := runFake(t, imp)

	if res.extracted != 2 {
		t.Errorf("OnURLsExtracted(%d), want 2", res.extracted)
	}
	if !hasWarning(res.warnings, "1 of 2 videos failed") {
		t.Errorf("warnings = %q, want the dead video reported", res.warnings)
	}
	if want := []string{filepath.Join(res.tmp, "merged")}; fmt.Sprint(imp.dirs) != fmt.Sprint(want) {
		t.Errorf("imported %v, want %v", imp.dirs, want)
	}
	if len(imp.albums) != 1 || imp.albums[0] != nil {
		t.Errorf("albums = %v, want nil: a user playlist is not an album", imp.albums)
	}
	for _, name := range []string{"song.mp3", "song.lrc"} {
		if _, err := os.Stat(filepath.Join(res.out, "Artist", "Imported", name)); err != nil {
			t.Errorf("%s not in the library under the imported album: %v", name, err)
		}
	}
	if files := libraryFiles(t, res.out); len(files) != 1 {
		t.Errorf("library holds %v, want only song.mp3: the failed download's leftover must stay out", files)
	}
}

func TestRunFilesTheTracksWhenImportFails(t *testing.T) {
	res := runFake(t, &retagger{err: fmt.Errorf("all 1 files failed metadata resolution")})

	if !hasWarning(res.warnings, "metadata resolution failed") {
		t.Errorf("warnings = %q, want the failed resolution reported", res.warnings)
	}
	if _, err := os.Stat(filepath.Join(res.out, "Artist", "Imported", "song.mp3")); err != nil {
		t.Errorf("song.mp3 not moved after a failed import: %v", err)
	}
}

func TestRunHandsAnAlbumPlaylistToTheImportAsAnAlbum(t *testing.T) {
	imp := &retagger{}
	res := runFakePlaylist(t, imp, "OLAK5uy_x", "Album - Record")

	if len(imp.albums) != 1 || imp.albums[0] == nil {
		t.Fatalf("albums = %v, want the album", imp.albums)
	}
	album := imp.albums[0]
	if album.Title != "Record" || fmt.Sprint(album.Tracks) != "[Song Dead]" {
		t.Errorf("album = %q %q, want Record [Song Dead]", album.Title, album.Tracks)
	}
	// The dead video keeps its place in the album but has no file.
	want := map[string]int{filepath.Join(res.tmp, "merged", "song.mp3"): 1}
	if fmt.Sprint(album.Files) != fmt.Sprint(want) {
		t.Errorf("files = %v, want %v: the paths after merging", album.Files, want)
	}
}

func TestRunCancelledDuringDownloadMovesNothing(t *testing.T) {
	cfg, hooks, res := setupFakeRun(t, "PLx", "Mix")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hooks.OnProgress = cancel
	imp := &retagger{}

	err := run(ctx, cfg, logger.New(false), res.tmp, hooks, imp, songLyrics())

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want the cancellation", err)
	}
	if len(imp.dirs) != 0 {
		t.Errorf("imported %v after the run was cancelled", imp.dirs)
	}
	if files := libraryFiles(t, res.out); len(files) != 0 {
		t.Errorf("library holds %v after a cancelled run", files)
	}
}

// Cancels the run from inside the resolution, as a user pressing Cancel while tags are being resolved.
type cancellingImporter struct{ cancel context.CancelFunc }

func (c cancellingImporter) Import(ctx context.Context, _ string, _ *metadata.Album) error {
	c.cancel()
	return fmt.Errorf("resolving metadata: %w", ctx.Err())
}

func TestRunCancelledDuringImportMovesNothing(t *testing.T) {
	cfg, hooks, res := setupFakeRun(t, "PLx", "Mix")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err := run(ctx, cfg, logger.New(false), res.tmp, hooks, cancellingImporter{cancel}, songLyrics())

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want the cancellation", err)
	}
	if files := libraryFiles(t, res.out); len(files) != 0 {
		t.Errorf("library holds %v after a cancelled run", files)
	}
	if hasWarning(res.warnings, "metadata resolution failed") {
		t.Errorf("warnings = %q: a cancellation is not a failed resolution", res.warnings)
	}
}

func TestRunReportsFilesItCouldNotMove(t *testing.T) {
	cfg, hooks, res := setupFakeRun(t, "PLx", "Mix")
	// A file where the artist's directory should go makes the move fail.
	if err := os.WriteFile(filepath.Join(res.out, "Artist"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(t.Context(), cfg, logger.New(false), res.tmp, hooks, &retagger{}, songLyrics()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !hasWarning(res.warnings, "file not moved to the library") {
		t.Errorf("warnings = %q, want the file that stayed out of the library reported", res.warnings)
	}
}

// Two uploads with one title used to share a path, and the second overwrote the first.
func TestRunKeepsVideosWithTheSameTitleAndDownloadsARepeatOnce(t *testing.T) {
	cfg, hooks, res := setupFakeRun(t, "PLx", "Mix")
	var listing []string
	for _, id := range []string{"a", "b", "c", "c"} {
		listing = append(listing, fmt.Sprintf(`{"webpage_url": "https://www.youtube.com/watch?v=%s", "title": "Same"}`, id))
	}
	fixture := trackFile(t, t.TempDir(), "fixture.mp3", map[string][]string{taglib.Title: {"Same"}, taglib.Artist: {"Artist"}})
	calls := writeFakeYtdlp(t, strings.Join(listing, "\n"), fixture, "Same")

	if err := run(t.Context(), cfg, logger.New(false), res.tmp, hooks, &retagger{}, songLyrics()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := libraryFiles(t, res.out); len(got) != 3 {
		t.Errorf("library = %v, want one file per video", got)
	}
	if res.extracted != 3 {
		t.Errorf("extracted = %d, want 3 distinct videos", res.extracted)
	}
	ran, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(ran), "\n"); n != 3 {
		t.Errorf("yt-dlp downloaded %d times, want 3:\n%s", n, ran)
	}
}
