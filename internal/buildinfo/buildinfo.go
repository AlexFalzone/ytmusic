package buildinfo

// Set with -ldflags "-X ytmusic/internal/buildinfo.Version=…".
var Version = "dev"

// MusicBrainz throttles clients whose User-Agent lacks "App/version ( contact )".
func UserAgent() string {
	return "ytmusic/" + Version + " ( https://github.com/AlexFalzone/ytmusic )"
}
