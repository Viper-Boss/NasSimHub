package callmedia

import (
	"errors"
	"net"
	"testing"
)

func TestValidateOfferRequiresOneFullDuplexOpusAudioSection(t *testing.T) {
	if err := validateOfferSDP(minimalSDP(opusMedia("sendrecv"))); err != nil {
		t.Fatalf("valid Opus offer rejected: %v", err)
	}

	pcmu := "m=audio 9 UDP/TLS/RTP/SAVPF 0\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=sendrecv\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	if err := validateOfferSDP(minimalSDP(pcmu)); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("PCMU-only error = %v", err)
	}
	if err := validateOfferSDP(minimalSDP(opusMedia("sendonly"))); !errors.Is(err, ErrUnsupportedDirection) {
		t.Fatalf("one-way audio error = %v", err)
	}
	conflictingDirection := opusMedia("sendrecv") + "a=recvonly\r\n"
	if err := validateOfferSDP(minimalSDP(conflictingDirection)); !errors.Is(err, ErrInvalidOffer) {
		t.Fatalf("conflicting direction error = %v", err)
	}
	disabled := "m=audio 0 UDP/TLS/RTP/SAVPF 111\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=sendrecv\r\n" +
		"a=rtpmap:111 opus/48000/2\r\n"
	if err := validateOfferSDP(minimalSDP(disabled)); !errors.Is(err, ErrUnsupportedDirection) {
		t.Fatalf("disabled audio error = %v", err)
	}

	video := "m=video 9 UDP/TLS/RTP/SAVPF 96\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=rtpmap:96 VP8/90000\r\n"
	if err := validateOfferSDP(minimalSDP(opusMedia("sendrecv"), video)); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("audio plus video error = %v", err)
	}
	if err := validateOfferSDP(minimalSDP(video)); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("video-only error = %v", err)
	}
}

func TestValidateOfferRejectsInvalidPacketTime(t *testing.T) {
	media := opusMedia("sendrecv") + "a=ptime:15\r\n"
	if err := validateOfferSDP(minimalSDP(media)); !errors.Is(err, ErrInvalidOffer) {
		t.Fatalf("15 ms ptime error = %v", err)
	}
	media = opusMedia("sendrecv") + "a=ptime:60\r\na=maxptime:20\r\n"
	if err := validateOfferSDP(minimalSDP(media)); !errors.Is(err, ErrInvalidOffer) {
		t.Fatalf("ptime over maxptime error = %v", err)
	}
}

func TestNormalizePublicIPs(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{
			name:  "empty input yields no addresses",
			input: nil,
			want:  nil,
		},
		{
			name:  "blank entries are dropped",
			input: []string{"", "  ", "\t"},
			want:  nil,
		},
		{
			name:  "addresses are trimmed",
			input: []string{" 192.168.120.190 "},
			want:  []string{"192.168.120.190"},
		},
		{
			name:  "ipv6 is accepted",
			input: []string{"2409:8a28:d31:db70::71e"},
			want:  []string{"2409:8a28:d31:db70::71e"},
		},
		{
			name:  "several addresses keep their order",
			input: []string{"192.168.120.190", "10.0.0.1"},
			want:  []string{"192.168.120.190", "10.0.0.1"},
		},
		{
			name:    "a hostname is rejected rather than silently ignored",
			input:   []string{"nas.local"},
			wantErr: true,
		},
		{
			name:    "an out-of-range address is rejected",
			input:   []string{"192.168.120.999"},
			wantErr: true,
		},
		{
			name:    "a valid address next to a typo still fails",
			input:   []string{"192.168.120.190", "not-an-ip"},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizePublicIPs(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizePublicIPs(%v) error = nil, want error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePublicIPs(%v) error = %v", test.input, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("normalizePublicIPs(%v) = %v, want %v", test.input, got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("normalizePublicIPs(%v) = %v, want %v", test.input, got, test.want)
				}
			}
		})
	}
}

// The default path must stay byte-for-byte compatible: an unconfigured host
// gets the same API it always did, with a no-op closer.
func TestNewWebRTCAPIWithoutMediaOverridesKeepsDefaultBehaviour(t *testing.T) {
	api, closer, err := newWebRTCAPI(nil, 0)
	if err != nil {
		t.Fatalf("newWebRTCAPI without overrides: %v", err)
	}
	if api == nil {
		t.Fatal("newWebRTCAPI returned a nil API")
	}
	if closer == nil {
		t.Fatal("newWebRTCAPI returned a nil closer")
	}
	if err := closer(); err != nil {
		t.Fatalf("default closer returned %v, want nil", err)
	}
}

func TestNewWebRTCAPIRejectsInvalidPublicIP(t *testing.T) {
	if _, _, err := newWebRTCAPI([]string{"nas.local"}, 0); err == nil {
		t.Fatal("newWebRTCAPI accepted a hostname, want error")
	}
}

// A configured UDP port must actually bind so the operator can map exactly one
// container port. Port 0 is not used here because it would mean "unset".
func TestNewWebRTCAPIWithUDPPortCreatesClosableMux(t *testing.T) {
	port := freeUDPPort(t)
	api, closer, err := newWebRTCAPI([]string{"192.168.120.190"}, port)
	if err != nil {
		t.Fatalf("newWebRTCAPI with UDP mux: %v", err)
	}
	if api == nil {
		t.Fatal("newWebRTCAPI returned a nil API")
	}
	if err := closer(); err != nil {
		t.Fatalf("mux closer returned %v, want nil", err)
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("reserve a UDP port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	if err := conn.Close(); err != nil {
		t.Fatalf("release the reserved UDP port: %v", err)
	}
	return port
}
