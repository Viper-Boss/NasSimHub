package nassimhubnode

import (
	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"net/url"
	"strings"
	"testing"
)

func TestManagementLinkUsesFragmentAndDropsUntrustedComponents(t *testing.T) {
	access := nodeclient.LocalAdminAccess{SetupCode: strings.Repeat("x", 43), Port: 7581}
	link, err := managementLink("https://192.0.2.5:7580/old?unsafe=1#old", access)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.URL)
	if u.Host != "192.0.2.5:7581" || u.Path != "/" || u.RawQuery != "" || u.Fragment != "setup="+access.SetupCode {
		t.Fatal("unsafe management link")
	}
	access.Configured = true
	link, err = managementLink("https://[2001:db8::1]:7580", access)
	u, _ = url.Parse(link.URL)
	if err != nil || u.Fragment != "" || u.Host != "[2001:db8::1]:7581" {
		t.Fatal("configured link exposed proof or broke IPv6")
	}
	access.Configured = false
	for _, base := range []string{"http://device:7580", "https://user:pass@device:7580", "https://"} {
		if _, err = managementLink(base, access); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	access.SetupCode = "invalid"
	if _, err = managementLink("https://device:7580", access); err == nil {
		t.Fatal("invalid proof accepted")
	}
}
