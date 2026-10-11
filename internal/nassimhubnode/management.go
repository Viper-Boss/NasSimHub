package nassimhubnode

import (
	"context"
	"errors"
	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"net"
	"net/url"
	"regexp"
	"strconv"
)

type ManagementLink struct {
	URL        string `json:"url"`
	Configured bool   `json:"configured"`
}

var managementProof = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func managementLink(base string, access nodeclient.LocalAdminAccess) (ManagementLink, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || access.Port < 1 || access.Port > 65535 {
		return ManagementLink{}, errors.New("invalid device management endpoint")
	}
	if !access.Configured && !managementProof.MatchString(access.SetupCode) {
		return ManagementLink{}, errors.New("device did not provide a valid setup proof")
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(access.Port))
	u.Path = "/"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.RawFragment = ""
	if !access.Configured {
		u.Fragment = url.Values{"setup": {access.SetupCode}}.Encode()
	}
	return ManagementLink{URL: u.String(), Configured: access.Configured}, nil
}

// LocalManagement never adopts a device or replaces ownership. Only an
// existing online paired device can supply its first-setup proof.
func (f *Fleet) LocalManagement(ctx context.Context, deviceID string) (ManagementLink, error) {
	live := f.lookup(deviceID)
	if live == nil {
		return ManagementLink{}, ErrUnknownNode
	}
	live.mu.Lock()
	state, record := live.state, live.record
	live.mu.Unlock()
	if !state.Online || state.Revoked || !state.Compatible {
		return ManagementLink{}, ErrNodeUnavailable
	}
	client, _ := live.handles()
	access, err := client.LocalAdministration(ctx)
	if err != nil {
		return ManagementLink{}, err
	}
	// Browser clients on the NAS LAN can reach Wi-Fi directly. USB is the
	// fallback for a computer connected directly to the stick.
	endpoint := record.WiFiEndpoint
	if endpoint == "" {
		endpoint = record.USBEndpoint
	}
	return managementLink(endpoint, access)
}

// USB management opens the NAS proxy path, including a one-use setup fragment
// only when the device has not yet initialized its own administrator password.
func (f *Fleet) LocalUSBManagement(ctx context.Context, id string) (ManagementLink, error) {
	live := f.lookup(id)
	if live == nil {
		return ManagementLink{}, ErrUnknownNode
	}
	live.mu.Lock()
	state, record := live.state, live.record
	live.mu.Unlock()
	if !state.Online || state.Revoked || !state.Compatible || record.USBEndpoint != "https://10.55.0.2:7580" {
		return ManagementLink{}, ErrNodeUnavailable
	}
	client, _ := live.handles()
	access, e := client.LocalAdministration(ctx)
	if e != nil {
		return ManagementLink{}, e
	}
	if len(access.CertificateDER) == 0 {
		return ManagementLink{}, errors.New("device needs USB console support")
	}
	link, e := managementLink(record.USBEndpoint, access)
	if e != nil {
		return ManagementLink{}, e
	}
	u, e := url.Parse(link.URL)
	if e != nil {
		return ManagementLink{}, e
	}
	u.Scheme = ""
	u.Host = ""
	u.Path = "/sim-nodes/setting/" + id + "/"
	link.URL = u.String()
	return link, nil
}
