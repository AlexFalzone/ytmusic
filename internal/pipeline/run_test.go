package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/lyrics"
	"ytmusic/pkg/utils"

	"go.senan.xyz/taglib"
)

// Lists "song" and "dead": "song" copies fixture where -o points and reports it, "dead" fails like an unavailable video.
func fakeYtdlp(t *testing.T, fixture string) {
	t.Helper()
	script := `#!/bin/sh
for a in "$@"; do last="$a"; done
case " $* " in
*" --flat-playlist "*)
	echo "https://www.youtube.com/watch?v=song"
	echo "https://www.youtube.com/watch?v=dead"
	exit 0;;
esac
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
if [ "$id" = "dead" ]; then echo "ERROR: Video unavailable" >&2; exit 1; fi
out=$(printf '%s' "$tmpl" | sed -e 's/%(artist)s/Artist/' -e 's/%(album)s/Album/' -e "s/%(title)s/$id/" -e 's/%(ext)s/mp3/')
mkdir -p "$(dirname "$out")"
/bin/cp -f "` + fixture + `" "$out"
printf '%s\n' "$out" > "$list"
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Renames every album, so the test can tell files were imported before being moved.
type retagger struct {
	dirs []string
	err  error
}

func (r *retagger) Import(_ context.Context, dir string) error {
	r.dirs = append(r.dirs, dir)
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
	fixture := trackFile(t, t.TempDir(), "fixture.mp3", map[string][]string{
		taglib.Title: {"Song"}, taglib.Artist: {"Artist"}, taglib.Album: {"Album"},
	})
	fakeYtdlp(t, fixture)

	res := runResult{out: t.TempDir(), tmp: t.TempDir()}
	cfg := config.DefaultConfig()
	cfg.PlaylistURL = "https://www.youtube.com/playlist?list=x"
	cfg.OutputDir = res.out
	cfg.ParallelJobs = 1
	hooks := Hooks{
		OnURLsExtracted: func(total int) { res.extracted = total },
		OnWarning:       func(msg string) { res.warnings = append(res.warnings, msg) },
	}
	lf := &fakeLyrics{byTitle: map[string]lyrics.Result{"Song": {Synced: "[00:01.00]la"}}}

	if err := run(context.Background(), cfg, logger.New(false), res.tmp, hooks, imp, lf); err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
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
	for _, name := range []string{"song.mp3", "song.lrc"} {
		if _, err := os.Stat(filepath.Join(res.out, "Artist", "Imported", name)); err != nil {
			t.Errorf("%s not in the library under the imported album: %v", name, err)
		}
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
