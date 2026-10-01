package metadata

import (
	"context"
	"testing"
	"time"

	"ytmusic/internal/logger"

	"go.senan.xyz/taglib"
)

// corpusProvider answers from fixtures keyed by the query's title, so every
// file of the corpus can be resolved in one run without any network.
type corpusProvider struct {
	name    string
	byTitle map[string][]TrackInfo
}

func (p corpusProvider) Name() string { return p.name }

func (p corpusProvider) Search(_ context.Context, q SearchQuery) ([]TrackInfo, error) {
	return p.byTitle[q.Title], nil
}

// corpusCase is one hard case seen in real use. key is the title the query
// must carry once cleaned: a provider only answers to that exact title.
type corpusCase struct {
	name          string
	title, artist string
	seconds       string // the file's length
	key           string
	first, second []TrackInfo
	want          map[string]string // tag → value after resolution, "" for absent
}

// unchanged is what a file keeps when no candidate is accepted.
func unchanged(title, artist string) map[string]string {
	return map[string]string{
		taglib.Title: title, taglib.Artist: artist,
		taglib.Album: "", taglib.ISRC: "", taglib.TrackNumber: "",
	}
}

var corpus = []corpusCase{
	{
		name:  "featuring credit on both sides",
		title: "Peaches (feat. Daniel Caesar & Giveon)", artist: "Justin Bieber", seconds: "1",
		key: "Peaches",
		first: []TrackInfo{{
			Title: "Peaches (feat. Daniel Caesar & Giveon)", Artist: "Justin Bieber, Daniel Caesar, Giveon",
			Album: "Justice", ISRC: "USUM72102471", TrackNumber: 15,
		}},
		want: map[string]string{
			taglib.Title: "Peaches (feat. Daniel Caesar & Giveon)", taglib.Artist: "Justin Bieber, Daniel Caesar, Giveon",
			taglib.Album: "Justice", taglib.ISRC: "USUM72102471", taglib.TrackNumber: "15",
		},
	},
	{
		// The live take lasts exactly as long as the file: only the version
		// check keeps it out.
		name:  "live take of a studio file",
		title: "Bohemian Rhapsody", artist: "Queen", seconds: "355",
		key: "Bohemian Rhapsody",
		first: []TrackInfo{
			{Title: "Bohemian Rhapsody - Live at Wembley '86", Artist: "Queen", Album: "Live at Wembley '86", Duration: 355 * time.Second},
			{
				Title: "Bohemian Rhapsody - Remastered 2011", Artist: "Queen", Album: "A Night at the Opera",
				Duration: 354 * time.Second, ISRC: "GBUM71029604", TrackNumber: 11,
			},
		},
		want: map[string]string{
			taglib.Title: "Bohemian Rhapsody - Remastered 2011", taglib.Artist: "Queen",
			taglib.Album: "A Night at the Opera", taglib.ISRC: "GBUM71029604", taglib.TrackNumber: "11",
		},
	},
	{
		name:  "only a remix on offer",
		title: "Levitating", artist: "Dua Lipa", seconds: "1",
		key:   "Levitating",
		first: []TrackInfo{{Title: "Levitating (The Blessed Madonna Remix)", Artist: "Dua Lipa", Album: "Club Future Nostalgia"}},
		want:  unchanged("Levitating", "Dua Lipa"),
	},
	{
		name:  "sped up variant borrows the original",
		title: "Save Your Tears (Sped Up)", artist: "The Weeknd", seconds: "150",
		key: "Save Your Tears",
		first: []TrackInfo{{
			Title: "Save Your Tears", Artist: "The Weeknd", Album: "After Hours",
			Duration: 215 * time.Second, ISRC: "USUG12000658", TrackNumber: 11, Year: 2020,
		}},
		want: map[string]string{
			taglib.Title: "Save Your Tears (Sped Up)", taglib.Artist: "The Weeknd",
			taglib.Album: "After Hours", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
	{
		name:  "file much shorter than the recording",
		title: "Shape of You", artist: "Ed Sheeran", seconds: "120",
		key:   "Shape of You",
		first: []TrackInfo{{Title: "Shape of You", Artist: "Ed Sheeran", Album: "÷ (Deluxe)", Duration: 233 * time.Second}},
		want:  unchanged("Shape of You", "Ed Sheeran"),
	},
	{
		name:  "music video longer than the recording",
		title: "Bad Guy", artist: "Billie Eilish", seconds: "230",
		key:   "Bad Guy",
		first: []TrackInfo{{Title: "bad guy", Artist: "Billie Eilish", Album: "WHEN WE ALL FALL ASLEEP, WHERE DO WE GO?", Duration: 194 * time.Second}},
		want: map[string]string{
			taglib.Title: "bad guy", taglib.Artist: "Billie Eilish",
			taglib.Album: "WHEN WE ALL FALL ASLEEP, WHERE DO WE GO?", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
	{
		name:  "short words one letter apart",
		title: "Lock", artist: "The Locksmiths", seconds: "1",
		key:   "Lock",
		first: []TrackInfo{{Title: "Clock", Artist: "The Locksmiths", Album: "Clockwork"}},
		want:  unchanged("Lock", "The Locksmiths"),
	},
	{
		name:  "missing accent",
		title: "Piu bella cosa", artist: "Eros Ramazzotti", seconds: "1",
		key:   "Piu bella cosa",
		first: []TrackInfo{{Title: "Più bella cosa", Artist: "Eros Ramazzotti", Album: "Dove c'è musica"}},
		want: map[string]string{
			taglib.Title: "Più bella cosa", taglib.Artist: "Eros Ramazzotti",
			taglib.Album: "Dove c'è musica", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
	{
		name:  "ampersand against and",
		title: "The Boxer", artist: "Simon and Garfunkel", seconds: "1",
		key:   "The Boxer",
		first: []TrackInfo{{Title: "The Boxer", Artist: "Simon & Garfunkel", Album: "Bridge Over Troubled Water"}},
		want: map[string]string{
			taglib.Title: "The Boxer", taglib.Artist: "Simon & Garfunkel",
			taglib.Album: "Bridge Over Troubled Water", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
	{
		name:  "stacked live and remaster suffixes",
		title: "Comfortably Numb", artist: "Pink Floyd", seconds: "382",
		key: "Comfortably Numb",
		first: []TrackInfo{
			{Title: "Comfortably Numb - Live; 2000 Remaster", Artist: "Pink Floyd", Album: "Is There Anybody Out There?", Duration: 382 * time.Second},
			{Title: "Comfortably Numb", Artist: "Pink Floyd", Album: "The Wall", Duration: 383 * time.Second},
		},
		want: map[string]string{
			taglib.Title: "Comfortably Numb", taglib.Artist: "Pink Floyd",
			taglib.Album: "The Wall", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
	{
		// No artist tag: the hyphen inside the title must not be read as an
		// "Artist - Title" separator.
		name:  "hyphenated title without an artist",
		title: "Anti-Hero", artist: "", seconds: "1",
		key:   "Anti-Hero",
		first: []TrackInfo{{Title: "Anti-Hero", Artist: "Taylor Swift", Album: "Midnights"}},
		want: map[string]string{
			taglib.Title: "Anti-Hero", taglib.Artist: "Taylor Swift",
			taglib.Album: "Midnights", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
	{
		name:  "exact variant from a later provider beats the donor",
		title: "Yellow (Live)", artist: "Coldplay", seconds: "1",
		key:    "Yellow",
		first:  []TrackInfo{{Title: "Yellow", Artist: "Coldplay", Album: "Parachutes", ISRC: "GBAYE0000351"}},
		second: []TrackInfo{{Title: "Yellow - Live", Artist: "Coldplay", Album: "Live 2003"}},
		want: map[string]string{
			taglib.Title: "Yellow - Live", taglib.Artist: "Coldplay",
			taglib.Album: "Live 2003", taglib.ISRC: "", taglib.TrackNumber: "",
		},
	},
}

// TestResolveCorpus resolves every hard case in a single run, the way a
// playlist is resolved, and checks what each file ends up tagged with.
func TestResolveCorpus(t *testing.T) {
	first := corpusProvider{name: "first", byTitle: map[string][]TrackInfo{}}
	second := corpusProvider{name: "second", byTitle: map[string][]TrackInfo{}}
	paths := make([]string, len(corpus))

	for i, c := range corpus {
		if _, dup := first.byTitle[c.key]; dup {
			t.Fatalf("%s: key %q used twice", c.name, c.key)
		}
		first.byTitle[c.key] = c.first
		second.byTitle[c.key] = c.second

		paths[i] = newTestMP3Len(t, c.seconds)
		tagTestFile(t, paths[i], c.title, c.artist)
	}

	r := NewResolver([]Provider{first, second}, logger.New(false), 0)
	if err := r.Resolve(context.Background(), paths); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for i, c := range corpus {
		for key, want := range c.want {
			if got := readTestTag(t, paths[i], key); got != want {
				t.Errorf("%s: %s = %q, want %q", c.name, key, got, want)
			}
		}
	}
}
