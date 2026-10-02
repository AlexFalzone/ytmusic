package buildinfo

import (
	"strings"
	"testing"
)

func TestUserAgentNamesVersionAndContact(t *testing.T) {
	ua := UserAgent()

	if !strings.HasPrefix(ua, "ytmusic/"+Version+" ") {
		t.Errorf("UserAgent() = %q, want it to start with ytmusic/%s", ua, Version)
	}
	if !strings.HasSuffix(ua, "( https://github.com/AlexFalzone/ytmusic )") {
		t.Errorf("UserAgent() = %q, want a contact URL in parentheses", ua)
	}
}
