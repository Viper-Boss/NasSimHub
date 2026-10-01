package communication

import (
	"github.com/human-agent65535/modemdeck/internal/store"
	"testing"
)

func TestVerifiedNodeMediaDoesNotRequireHostAudio(t *testing.T) {
	for _, test := range []struct {
		endpoint    string
		media, want bool
	}{{"nsh:node-1:0", true, true}, {"nsh:node-1:0", false, false}, {"host-1", true, false}} {
		status := Status{Lines: []store.LineSummary{{EndpointID: test.endpoint, Capabilities: store.LineCapabilities{Media: test.media}}}}
		includeNodeMediaCapability(&status)
		if status.Capabilities.Media != test.want {
			t.Fatalf("endpoint=%s media=%v got=%v", test.endpoint, test.media, status.Capabilities.Media)
		}
	}
}
