package pipeline

import (
	"testing"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
	"ytmusic/internal/metadata"
)

// Checked as the importer receives it. A nil *Fingerprinter stored in an
// interface is not a nil interface: it passes the importer's nil check and the
// first file crashes on a nil receiver.
func TestNoFingerprinterWithoutAcoustID(t *testing.T) {
	c := buildComponents(config.Config{MetadataProviders: []string{"deezer"}}, logger.New(false))

	var fp metadata.Fingerprinter = c.fingerprinter
	if fp != nil {
		t.Fatalf("fingerprinter = %#v, want none without an AcoustID key", fp)
	}
}

func TestFingerprinterWithAcoustID(t *testing.T) {
	c := buildComponents(config.Config{AcoustIDAPIKey: "key"}, logger.New(false))

	if c.fingerprinter == nil {
		t.Fatal("fingerprinter missing with an AcoustID key")
	}
}
