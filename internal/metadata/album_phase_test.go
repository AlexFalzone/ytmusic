package metadata

import "testing"

func tracklist(id, status, date string, titles ...string) Tracklist {
	tl := Tracklist{ID: id, Status: status, Date: date}
	for i, title := range titles {
		tl.Tracks = append(tl.Tracks, ReleaseTrack{TrackNumber: i + 1, DiscNumber: 1, Title: title})
	}
	return tl
}

var divineFeminine = []string{
	"Congratulations (feat. Bilal)", "Dang! (feat. Anderson .Paak)", "Stay", "Skin", "Cinderella (feat. Ty Dolla $ign)",
	"Planet God Damn (feat. Njomza)", "Soulmate", "We (feat. CeeLo Green)", "My Favorite Part",
	"God Is Fair, Sexy Nasty (feat. Kendrick Lamar)", "Grand", "Channel 13", "Butterflies",
}

func TestMatchReleaseToleratesOneTitleOfNineteen(t *testing.T) {
	album := []string{"Over My Dead Body", "Shot For Me", "Headlines (Explicit Version)", "Crew Love", "Take Care",
		"Marvins Room", "Buried Alive Interlude", "Under Ground Kings", "We'll Be Fine", "Make Me Proud", "Lord Knows",
		"Cameras / Good Ones Go Interlude (Medley)", "Doing It Wrong", "The Real Her", "Look What You've Done",
		"HYFR (Hell Ya Fucking Right)", "Practice", "The Ride", "The Motto"}
	release := tracklist("deluxe", "Official", "2012-03-20", "Over My Dead Body", "Shot for Me", "Headlines", "Crew Love",
		"Take Care", "Marvins Room", "Buried Alive Interlude", "Under Ground Kings", "We’ll Be Fine", "Make Me Proud",
		"Lord Knows", "Cameras / Good Ones Go Interlude (medley)", "Doing It Wrong", "The Real Her",
		"Look What You’ve Done", "HYFR (Hell Ya Fucking Right)", "Practice", "The Ride", "The Motto")

	got, ok := matchRelease(album, []Tracklist{release})
	if !ok || got.ID != "deluxe" {
		t.Errorf("matchRelease = %q, %v; want the deluxe release despite one title that differs", got.ID, ok)
	}
}

// A bootleg sharing the first ten titles is another edition.
func TestMatchReleaseRejectsAnotherEditionWithTheSameCount(t *testing.T) {
	bootleg := tracklist("bootleg", "Bootleg", "2019", "Congratulations", "Dang!", "Stay", "Skin", "Cinderella",
		"Planet God Damn", "Soulmate", "We", "My Favorite Part", "God is Fair, Sexy Nasty",
		"Watch the Sun Come Up", "Say Goodbye", "The Sun Room")

	if got, ok := matchRelease(divineFeminine, []Tracklist{bootleg}); ok {
		t.Errorf("matchRelease chose %q: three titles of thirteen differ", got.ID)
	}
}

func TestMatchReleaseRejectsReversedOrder(t *testing.T) {
	album := []string{"BLOOD.", "DNA.", "YAH.", "ELEMENT."}
	reversed := tracklist("collectors", "Official", "2017-12-08", "ELEMENT.", "YAH.", "DNA.", "BLOOD.")

	if got, ok := matchRelease(album, []Tracklist{reversed}); ok {
		t.Errorf("matchRelease chose %q: same titles, opposite order", got.ID)
	}
}

func TestMatchReleaseRejectsAnotherTrackCount(t *testing.T) {
	standard := tracklist("standard", "Official", "2016-09-16", divineFeminine[:10]...)

	if got, ok := matchRelease(divineFeminine, []Tracklist{standard}); ok {
		t.Errorf("matchRelease chose %q: 10 tracks, the album has 13", got.ID)
	}
}

func TestMatchReleaseIgnoresUnknownTitles(t *testing.T) {
	album := []string{"Stay", "", "Skin", ""}
	release := tracklist("r", "Official", "", "Stay", "Anything", "Skin", "Else")

	if _, ok := matchRelease(album, []Tracklist{release}); !ok {
		t.Error("the two known titles match: positions without a title must not count against the release")
	}
}

func TestMatchReleaseTieBreak(t *testing.T) {
	album := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India", "Juliett"}
	oneOff := append(append([]string{}, album[:9]...), "Kilo")
	tests := []struct {
		name       string
		candidates []Tracklist
		want       string
	}{
		{"higher ratio first", []Tracklist{
			tracklist("partial", "Official", "2016", oneOff...),
			tracklist("full", "Bootleg", "2019", album...),
		}, "full"},
		{"official over bootleg", []Tracklist{
			tracklist("boot", "Bootleg", "2001", album...),
			tracklist("off", "Official", "2016", album...),
		}, "off"},
		{"earliest date", []Tracklist{
			tracklist("late", "Official", "2020-05", album...),
			tracklist("early", "Official", "2016-09-16", album...),
		}, "early"},
		{"a date over none", []Tracklist{
			tracklist("undated", "Official", "", album...),
			tracklist("dated", "Official", "2020", album...),
		}, "dated"},
		{"lowest ID last", []Tracklist{
			tracklist("b", "Official", "2016", album...),
			tracklist("a", "Official", "2016", album...),
		}, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRelease(album, tt.candidates)
			if !ok || got.ID != tt.want {
				t.Errorf("matchRelease = %q, %v; want %q", got.ID, ok, tt.want)
			}
		})
	}
}
