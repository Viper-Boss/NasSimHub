// Package vowifi implements the honest reachability probes behind the VoWiFi
// switch: 3GPP ePDG discovery over DNS (with RCODE visibility), IKE liveness
// on UDP 500/4500, and a best-effort public egress (ISP) lookup.
//
// These probes measure the *external* gates of a VoWiFi session. They never
// claim the feature works; they report what was actually observed so the UI
// can explain why the switch is unavailable when it is.
package vowifi

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

const (
	defaultQueryTimeout = 3 * time.Second
	defaultIkeWait      = 3 * time.Second
	defaultEgressWait   = 4 * time.Second
	ikePort             = 500
	ikeNatPort          = 4500
)

// Request describes one diagnostics sweep.
type Request struct {
	FQDN string
	// CellularDNSServers are the carrier DNS servers from the connected
	// bearer; CellularInterface is the ECM data interface to bind to. When
	// either is empty the cellular path is skipped.
	CellularDNSServers []string
	CellularInterface  string
	// ResolvConf is the path of the system resolver config (default
	// /etc/resolv.conf); its servers define the broadband path.
	ResolvConf string
}

// Outcome carries the results of every probe in one sweep.
type Outcome struct {
	Cellular  domain.VowifiPathResult
	Broadband domain.VowifiPathResult
	Egress    domain.VowifiEgress
}

// Run executes the full diagnostics sweep. Every probe is best-effort and
// bounded by the request context; failures become descriptive results, never
// errors.
func Run(ctx context.Context, request Request) Outcome {
	outcome := Outcome{}
	if request.ResolvConf == "" {
		request.ResolvConf = "/etc/resolv.conf"
	}
	queryTimeout := defaultQueryTimeout

	broadbandServers := resolvConfServers(request.ResolvConf)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		outcome.Broadband = resolvePath(ctx, request.FQDN, broadbandServers, "", queryTimeout)
	}()
	go func() {
		defer wg.Done()
		outcome.Cellular = resolvePath(
			ctx,
			request.FQDN,
			request.CellularDNSServers,
			request.CellularInterface,
			queryTimeout,
		)
	}()
	wg.Wait()

	// IKE liveness: probe the first address we managed to discover, from the
	// default egress (that is the path a VoWiFi-over-broadband session would
	// take).
	target := outcome.Broadband.Addresses
	if len(target) == 0 {
		target = outcome.Cellular.Addresses
	}
	if len(target) > 0 {
		address := target[0]
		probeIke(ctx, &outcome.Broadband, address, defaultIkeWait)
	}
	outcome.Egress = lookupEgress(ctx, defaultEgressWait)
	return outcome
}

// resolvePath queries every DNS server of one path until one answers,
// returning the RCODE and A records it saw.
func resolvePath(
	ctx context.Context,
	fqdn string,
	servers []string,
	bindInterface string,
	timeout time.Duration,
) domain.VowifiPathResult {
	result := domain.VowifiPathResult{
		Addresses:  []string{},
		DNSServers: []string{},
	}
	if fqdn == "" {
		result.Error = "no discovery FQDN (serving operator unknown)"
		return result
	}
	if len(servers) == 0 {
		result.Error = "no DNS servers available on this path"
		return result
	}
	result.DNSServers = append(result.DNSServers, servers...)
	bound := 0
	for _, server := range servers {
		answer, err := dnsQuery(ctx, server, fqdn, bindInterface, timeout)
		if err != nil {
			if errors.Is(err, errBindUnavailable) && bindInterface != "" {
				// Keep going without binding: the answer is still the
				// closest approximation of this path.
				answer, err = dnsQuery(ctx, server, fqdn, "", timeout)
				if err != nil {
					result.Error = shortError(err)
					continue
				}
			} else {
				result.Error = shortError(err)
				continue
			}
		}
		bound++
		result.Available = true
		result.AnswerCode = answer.RcodeName()
		result.Addresses = append(result.Addresses, answer.Addresses...)
		if answer.RcodeName() == "NOERROR" && len(answer.Addresses) > 0 {
			result.Error = ""
			break
		}
	}
	if bound == 0 && result.Error == "" {
		result.Error = "no DNS server answered on this path"
	}
	if len(result.AnswerCode) > 0 && result.Error != "" {
		// A real RCODE was observed; the trailing error is secondary noise.
		result.Error = ""
	}
	return result
}

// dnsAnswer is a minimal parsed UDP DNS response.
type dnsAnswer struct {
	RCODE     int
	Addresses []string
}

func (a dnsAnswer) RcodeName() string {
	switch a.RCODE {
	case 0:
		return "NOERROR"
	case 1:
		return "FORMERR"
	case 2:
		return "SERVFAIL"
	case 3:
		return "NXDOMAIN"
	case 4:
		return "NOTIMP"
	case 5:
		return "REFUSED"
	default:
		return fmt.Sprintf("RCODE%d", a.RCODE)
	}
}

var errBindUnavailable = errors.New("SO_BINDTODEVICE unavailable")

// dnsQuery sends one raw A query over UDP and parses the reply.
func dnsQuery(
	ctx context.Context,
	server string,
	fqdn string,
	bindInterface string,
	timeout time.Duration,
) (dnsAnswer, error) {
	query, err := buildQuery(fqdn)
	if err != nil {
		return dnsAnswer{}, err
	}
	dialer := net.Dialer{Timeout: timeout}
	if bindInterface != "" {
		control, err := bindToDeviceControl(bindInterface)
		if err != nil {
			return dnsAnswer{}, err
		}
		dialer.Control = control
	}
	bindCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := dialer.DialContext(bindCtx, "udp", net.JoinHostPort(server, "53"))
	if err != nil {
		return dnsAnswer{}, fmt.Errorf("dns %s unreachable: %w", server, err)
	}
	defer connection.Close()
	if deadline, ok := bindCtx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := connection.Write(query); err != nil {
		return dnsAnswer{}, fmt.Errorf("dns query to %s failed: %w", server, err)
	}
	buffer := make([]byte, 2048)
	n, err := connection.Read(buffer)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return dnsAnswer{}, errors.New("dns timeout")
		}
		return dnsAnswer{}, fmt.Errorf("dns reply from %s failed: %w", server, err)
	}
	return parseAnswer(buffer[:n])
}

// buildQuery constructs an A-record query with recursion desired.
func buildQuery(fqdn string) ([]byte, error) {
	fqdn = strings.TrimSuffix(strings.TrimSpace(fqdn), ".")
	if fqdn == "" || len(fqdn) > 253 {
		return nil, errors.New("invalid discovery FQDN")
	}
	queryID := make([]byte, 2)
	if _, err := rand.Read(queryID); err != nil {
		return nil, err
	}
	query := []byte{
		queryID[0], queryID[1],
		0x01, 0x00, // flags: RD
		0x00, 0x01, // QDCOUNT
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	for _, label := range strings.Split(fqdn, ".") {
		if label == "" || len(label) > 63 {
			return nil, errors.New("invalid discovery FQDN label")
		}
		query = append(query, byte(len(label)))
		query = append(query, label...)
	}
	query = append(query, 0x00, 0x00, 0x01, 0x00, 0x01) // root, A, IN
	return query, nil
}

// parseAnswer extracts the RCODE and A records from a DNS reply.
func parseAnswer(reply []byte) (dnsAnswer, error) {
	if len(reply) < 12 {
		return dnsAnswer{}, errors.New("dns reply too short")
	}
	questionCount := int(binary.BigEndian.Uint16(reply[4:6]))
	answerCount := int(binary.BigEndian.Uint16(reply[6:8]))
	answer := dnsAnswer{RCODE: int(reply[3] & 0x0F), Addresses: []string{}}
	offset := 12
	for i := 0; i < questionCount && offset < len(reply); i++ {
		var err error
		if offset, err = skipName(reply, offset); err != nil {
			return answer, err
		}
		offset += 4 // QTYPE + QCLASS
	}
	for i := 0; i < answerCount && offset < len(reply); i++ {
		var err error
		if offset, err = skipName(reply, offset); err != nil {
			return answer, err
		}
		if offset+10 > len(reply) {
			break
		}
		recordType := binary.BigEndian.Uint16(reply[offset : offset+2])
		recordLength := int(binary.BigEndian.Uint16(reply[offset+8 : offset+10]))
		offset += 10
		if recordType == 1 && recordLength == 4 && offset+4 <= len(reply) {
			answer.Addresses = append(
				answer.Addresses,
				fmt.Sprintf("%d.%d.%d.%d", reply[offset], reply[offset+1], reply[offset+2], reply[offset+3]),
			)
		}
		offset += recordLength
	}
	return answer, nil
}

// skipName walks past a (possibly compressed) domain name.
func skipName(reply []byte, offset int) (int, error) {
	for {
		if offset >= len(reply) {
			return offset, errors.New("dns name truncated")
		}
		length := int(reply[offset])
		if length == 0 {
			return offset + 1, nil
		}
		if length&0xC0 == 0xC0 {
			if offset+2 > len(reply) {
				return offset, errors.New("dns compression pointer truncated")
			}
			return offset + 2, nil
		}
		offset += 1 + length
	}
}

// probeIke sends a minimal IKE_SA_INIT header to UDP 500/4500 and records
// whether the responder answered with anything at all.
func probeIke(ctx context.Context, result *domain.VowifiPathResult, address string, wait time.Duration) {
	result.Ike = domain.VowifiIkeResult{Attempted: true, Address: address, WaitMillis: int(wait.Milliseconds())}
	started := time.Now()
	packet := ikeInitPacket()
	result.Ike.Port500 = ikeProbe(ctx, address, ikePort, packet, wait)
	result.Ike.Port4500 = ikeProbe(ctx, address, ikeNatPort, packet, wait)
	result.Ike.Reachable = result.Ike.Port500 || result.Ike.Port4500
	if !result.Ike.Reachable {
		result.Ike.Error = fmt.Sprintf(
			"no IKE answer within %d ms (elapsed %d ms)",
			result.Ike.WaitMillis,
			time.Since(started).Milliseconds(),
		)
	}
}

// ikeInitPacket builds a 64-byte IKE header (SAi next-payload, v2,
// IKE_SA_INIT, initiator bit) with a random SPI. Real responders answer even
// a malformed body with a NOTIFY, which is all the liveness we need.
func ikeInitPacket() []byte {
	packet := make([]byte, 64)
	_, _ = rand.Read(packet[:8]) // initiator SPI
	// responder SPI stays zero; next payload 34 (SA), version 2.0,
	// exchange type 34 (IKE_SA_INIT), flags 0x08 (initiator).
	packet[16] = 34
	packet[17] = 0x20
	packet[18] = 34
	packet[19] = 0x08
	binary.BigEndian.PutUint32(packet[24:28], 64)
	return packet
}

func ikeProbe(ctx context.Context, address string, port int, packet []byte, wait time.Duration) bool {
	probeCtx, cancel := context.WithTimeout(ctx, wait+2*time.Second)
	defer cancel()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return false
	}
	defer connection.Close()
	remote, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(address, fmt.Sprint(port)))
	if err != nil {
		return false
	}
	if _, err := connection.WriteToUDP(packet, remote); err != nil {
		return false
	}
	if deadline, ok := probeCtx.Deadline(); ok {
		_ = connection.SetReadDeadline(deadline)
	}
	buffer := make([]byte, 1024)
	for {
		if _, _, err := connection.ReadFromUDP(buffer); err != nil {
			return false
		}
		return true
	}
}

// lookupEgress asks a public IP/ISP echo what the default route looks like.
// Best-effort only: unavailable egress simply stays unavailable.
func lookupEgress(ctx context.Context, timeout time.Duration) domain.VowifiEgress {
	egress := domain.VowifiEgress{}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://ip-api.com/json/?fields=status,isp,query",
		nil,
	)
	if err != nil {
		egress.Error = shortError(err)
		return egress
	}
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		egress.Error = shortError(err)
		return egress
	}
	defer response.Body.Close()
	var payload struct {
		Status string `json:"status"`
		ISP    string `json:"isp"`
		Query  string `json:"query"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		egress.Error = shortError(err)
		return egress
	}
	egress.Available = true
	egress.PublicIP = strings.TrimSpace(payload.Query)
	egress.ISP = strings.TrimSpace(payload.ISP)
	return egress
}

// resolvConfServers reads the nameservers of the system resolver.
func resolvConfServers(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	servers := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			server := strings.TrimSpace(fields[1])
			if server != "" {
				servers = append(servers, server)
			}
		}
	}
	return servers
}

func shortError(err error) string {
	message := err.Error()
	if len(message) > 160 {
		message = message[:160]
	}
	return message
}
