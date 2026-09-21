package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// The dispatch loop hands each NKN message to the receivers in turn and stops
// at the first one that claims it. These tests are about that boundary, in
// both directions: a real identity request must be recognised, and nothing
// else may be.

func TestLicenseIdentityRequestAccepted(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"type":    licenseIdentityType,
		"version": licenseIdentityVersion,
		"nonce":   "0011223344556677",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request, ok := parseLicenseIdentityRequest(payload)
	if !ok {
		t.Fatal("a well-formed identity request was not recognised")
	}
	if request.Nonce != "0011223344556677" {
		t.Fatalf("nonce not carried through: %q", request.Nonce)
	}
}

// TestLicenseIdentityLeavesOtherTrafficAlone is the one that protects the
// handlers around it. Every shape here belongs to another receiver or to
// nobody; claiming any of them would silently stop chat, DHT announcements,
// certificate deliveries or WireGuard endpoints from being processed at all.
func TestLicenseIdentityLeavesOtherTrafficAlone(t *testing.T) {
	foreign := map[string]any{
		"an NKN chat message": map[string]any{
			"type": "homesim.chat.message", "version": 1, "text": "hello",
		},
		"a DHT announcement": map[string]any{
			"type": "homesim.dht.announce", "version": 1,
		},
		"a WireGuard endpoint offer": map[string]any{
			"type": "homesim.wireguard.endpoint", "version": 1, "endpoint": "1.2.3.4:51820",
		},
		"a certificate delivery": map[string]any{
			"type": licenseDeliveryType, "version": licenseDeliveryVersion,
		},
		"a future version of this protocol": map[string]any{
			"type": licenseIdentityType, "version": licenseIdentityVersion + 1, "nonce": "0011223344556677",
		},
		"no nonce": map[string]any{
			"type": licenseIdentityType, "version": licenseIdentityVersion,
		},
		"a nonce too short to be random": map[string]any{
			"type": licenseIdentityType, "version": licenseIdentityVersion, "nonce": "0011",
		},
		"a nonce used as a payload": map[string]any{
			"type": licenseIdentityType, "version": licenseIdentityVersion, "nonce": strings.Repeat("a", 65),
		},
	}
	for name, shape := range foreign {
		t.Run(name, func(t *testing.T) {
			payload, err := json.Marshal(shape)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, ok := parseLicenseIdentityRequest(payload); ok {
				t.Fatal("claimed a message that is not an identity request")
			}
		})
	}

	t.Run("not JSON at all", func(t *testing.T) {
		if _, ok := parseLicenseIdentityRequest([]byte("this is not json")); ok {
			t.Fatal("claimed a message that is not JSON")
		}
	})
	t.Run("empty", func(t *testing.T) {
		if _, ok := parseLicenseIdentityRequest(nil); ok {
			t.Fatal("claimed an empty message")
		}
	})
}

// TestLicenseIdentityRefusesOversizedRequests covers the bound that keeps a
// large message from costing a parse. The check has to happen BEFORE
// json.Unmarshal or it buys nothing.
func TestLicenseIdentityRefusesOversizedRequests(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"type":    licenseIdentityType,
		"version": licenseIdentityVersion,
		"nonce":   "0011223344556677",
		"padding": strings.Repeat("x", licenseIdentityMaxRequestBytes),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) <= licenseIdentityMaxRequestBytes {
		t.Fatalf("test payload is only %d bytes; it was meant to exceed %d",
			len(payload), licenseIdentityMaxRequestBytes)
	}
	if _, ok := parseLicenseIdentityRequest(payload); ok {
		t.Fatal("accepted a request past the size bound")
	}
}

// TestLicenseIdentityReplyStaysWithinItsBound checks the other bound against a
// realistic worst case rather than an imagined one: a machine ID, a long
// machine error in Chinese, and more modules than any gateway this runs on has
// ports for. If a plausible reply can exceed the bound, the bound is what is
// wrong, and it should be found here rather than by a customer whose gateway
// answers every request with silence.
func TestLicenseIdentityReplyStaysWithinItsBound(t *testing.T) {
	modules := make([]string, 16)
	for index := range modules {
		modules[index] = "86743505123456" + string(rune('0'+index%10))
	}
	reply := licenseIdentityReply{
		Type:         licenseIdentityType,
		Version:      licenseIdentityVersion,
		Nonce:        strings.Repeat("f", 64),
		MachineID:    strings.Repeat("a", 64),
		MachineError: "存储未就绪，无法读取机器指纹：/sys/block/mmcblk0/device/cid 不存在或不可读",
		ModuleSNs:    modules,
		ReportedAt:   1750000000,
	}
	payload, err := json.Marshal(reply)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) > licenseIdentityMaxReplyBytes {
		t.Fatalf("a plausible reply is %d bytes, past the %d-byte bound",
			len(payload), licenseIdentityMaxReplyBytes)
	}
}
