package importer

import (
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/metadata"
	"ytmusic/internal/provider/musicbrainz"
)

// Checked as the importer receives it. A nil *Fingerprinter stored in an
// interface is not a nil interface: it passes the importer's nil check and the
// first file crashes on a nil receiver.
func TestNoFingerprinterWithoutAcoustID(t *testing.T) {
	c := buildComponents(config.Config{MetadataProviders: []string{"deezer"}})

	var fp metadata.Fingerprinter = c.fingerprinter
	if fp != nil {
		t.Fatalf("fingerprinter = %#v, want none without an AcoustID key", fp)
	}
}

func TestFingerprinterWithAcoustID(t *testing.T) {
	c := buildComponents(config.Config{AcoustIDAPIKey: "key"})

	if c.fingerprinter == nil {
		t.Fatal("fingerprinter missing with an AcoustID key")
	}
}

// One client per run: its throttle and its caches live in the instance, so a
// second one would double the request rate MusicBrainz allows. The AcoustID
// key matters: fingerprint lookups are the only other way to build a client.
func TestComponentsShareOneMusicBrainzClient(t *testing.T) {
	c := buildComponents(config.Config{
		MetadataProviders: []string{"deezer", "musicbrainz"},
		AcoustIDAPIKey:    "key",
	})

	mb, ok := c.providers[1].(*musicbrainz.Client)
	if !ok {
		t.Fatalf("provider 1 is %T, want *musicbrainz.Client", c.providers[1])
	}
	if c.albumResolver != metadata.AlbumResolver(mb) {
		t.Error("the album resolver is not the provider's MusicBrainz client")
	}
	if c.releaseResolver != metadata.ReleaseResolver(mb) {
		t.Error("the release resolver is not the provider's MusicBrainz client")
	}
}

// AcoustID alone still needs MusicBrainz, for the recordings it identifies.
func TestComponentsBuildMusicBrainzForAcoustIDAlone(t *testing.T) {
	c := buildComponents(config.Config{AcoustIDAPIKey: "key"})

	mb, ok := c.albumResolver.(*musicbrainz.Client)
	if !ok {
		t.Fatalf("album resolver is %T, want *musicbrainz.Client", c.albumResolver)
	}
	if c.releaseResolver != metadata.ReleaseResolver(mb) {
		t.Error("the release resolver is not the album resolver's MusicBrainz client")
	}
}
