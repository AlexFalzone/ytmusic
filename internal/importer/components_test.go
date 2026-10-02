package importer

import (
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/metadata"
	"ytmusic/internal/provider/musicbrainz"
)

// A nil *Fingerprinter in an interface passes the nil check and crashes on the first file.
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

// The AcoustID key matters: fingerprint lookups are the only other way to build a client.
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
