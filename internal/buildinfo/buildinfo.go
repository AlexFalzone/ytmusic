// Package buildinfo holds what the binary knows about its own build.
package buildinfo

// Version is set at build time:
//
//	go build -ldflags "-X ytmusic/internal/buildinfo.Version=v1.2.0"
var Version = "dev"

// UserAgent identifies ytmusic to the services it queries. MusicBrainz asks
// for "App/version ( contact )" and throttles clients that give no way to
// reach the maintainer.
func UserAgent() string {
	return "ytmusic/" + Version + " ( https://github.com/AlexFalzone/ytmusic )"
}
