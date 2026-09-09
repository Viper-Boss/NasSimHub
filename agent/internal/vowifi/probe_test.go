package vowifi

import (
	"encoding/binary"
	"testing"
)

func TestBuildQueryAndParseAnswer(t *testing.T) {
	query, err := buildQuery("epdg.epc.mnc011.mcc460.pub.3gppnetwork.org")
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	if len(query) < 17+38 {
		t.Fatalf("query unexpectedly short: %d bytes", len(query))
	}
	if got := binary.BigEndian.Uint16(query[2:4]) >> 8; got == 0 {
		// flags high byte 0x01 (RD) is fine; nothing to assert beyond shape.
		_ = got
	}

	// Build a minimal NOERROR reply with one A record.
	reply := make([]byte, 0, 64)
	header := []byte{0, 1, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0}
	reply = append(reply, header...)
	reply = append(reply, 1, 'a', 1, 'b', 0, 0, 1, 0, 1) // question a.b. A IN
	answerStart := len(reply)
	reply[answerStart-1] = 0                             // keep root label
	reply = append(reply, 0xC0, 12)                      // pointer to question name
	reply = append(reply, 0, 1, 0, 1)                    // type A, class IN
	reply = append(reply, 0, 0, 0, 60)                   // TTL
	reply = append(reply, 0, 4, 1, 2, 3, 4)              // RDLENGTH + A record
	parsed, err := parseAnswer(reply)
	if err != nil {
		t.Fatalf("parseAnswer failed: %v", err)
	}
	if parsed.RCODE != 0 {
		t.Fatalf("expected NOERROR, got %s", parsed.RcodeName())
	}
	if len(parsed.Addresses) != 1 || parsed.Addresses[0] != "1.2.3.4" {
		t.Fatalf("unexpected addresses: %v", parsed.Addresses)
	}
	if parsed.RcodeName() != "NOERROR" {
		t.Fatalf("unexpected rcode name: %s", parsed.RcodeName())
	}
}

func TestParseAnswerNXDOMAIN(t *testing.T) {
	reply := []byte{0, 1, 0x81, 0x83, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
	parsed, err := parseAnswer(reply)
	if err != nil {
		t.Fatalf("parseAnswer failed: %v", err)
	}
	if parsed.RcodeName() != "NXDOMAIN" {
		t.Fatalf("expected NXDOMAIN, got %s", parsed.RcodeName())
	}
	if len(parsed.Addresses) != 0 {
		t.Fatalf("expected no addresses, got %v", parsed.Addresses)
	}
}

func TestResolvConfServers(t *testing.T) {
	// Best-effort only: on a host without /etc/resolv.conf this stays empty.
	servers := resolvConfServers("/etc/resolv.conf")
	for _, server := range servers {
		if server == "" {
			t.Fatalf("empty server entry parsed: %v", servers)
		}
	}
}

func TestIkeInitPacketShape(t *testing.T) {
	packet := ikeInitPacket()
	if len(packet) != 64 {
		t.Fatalf("packet must be 64 bytes, got %d", len(packet))
	}
	if packet[17] != 0x20 || packet[18] != 34 || packet[19] != 0x08 {
		t.Fatalf("unexpected IKE header fields: %v", packet[16:20])
	}
	if got := binary.BigEndian.Uint32(packet[24:28]); got != 64 {
		t.Fatalf("length field must be 64, got %d", got)
	}
}
