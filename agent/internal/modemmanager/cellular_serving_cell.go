package modemmanager

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Serving-cell details come from AT+QENG="servingcell", which answers with a
// flat positional record. It is cached briefly because the dashboard polls it
// every few seconds and the modem round-trip is not free.

const (
	cellularCacheTTL     = 30 * time.Second
	cellularServingQuery = `AT+QENG="servingcell"`
)

// lteBandwidthMHz maps the QENG numeric bandwidth enum to MHz.
func lteBandwidthMHz(enum string) float64 {
	switch enum {
	case "0":
		return 1.4
	case "1":
		return 3
	case "2":
		return 5
	case "3":
		return 10
	case "4":
		return 15
	case "5":
		return 20
	default:
		return 0
	}
}

// lteDownlinkFrequencyMHz converts an LTE EARFCN to the downlink centre
// frequency for the bands common on Chinese networks. Returns 0 when unknown.
func lteDownlinkFrequencyMHz(earfcn uint32) float64 {
	type bandRange struct {
		offset uint32
		lowMHz float64
	}
	ranges := []bandRange{
		{0, 2110},     // B1
		{1200, 1805},  // B3
		{2400, 869},   // B5
		{2750, 2620},  // B7
		{3400, 937.5}, // B8
		{6150, 791},   // B20
		{9210, 758},   // B28
		{36200, 2010}, // B34
		{37750, 2573}, // B38
		{38250, 1880}, // B39
		{38650, 2300}, // B40
		{39650, 2496}, // B41
		{41590, 3400}, // B42
		{43990, 3600}, // B43
	}
	best := bandRange{}
	for _, candidate := range ranges {
		if earfcn >= candidate.offset && candidate.offset >= best.offset {
			best = candidate
		}
	}
	if best.lowMHz == 0 {
		return 0
	}
	return best.lowMHz + 0.1*float64(earfcn-best.offset)
}

// nrDownlinkFrequencyMHz converts an NR-ARFCN to the downlink centre
// frequency per 3GPP 38.104.
func nrDownlinkFrequencyMHz(arfcn uint32) float64 {
	switch {
	case arfcn < 300000:
		return 0.005 * float64(arfcn)
	case arfcn < 3700000:
		return 3000 + 0.015*float64(arfcn-600000)
	case arfcn < 3275000:
		return 24250 + float64(arfcn-3700000)*0.060
	default:
		return 0
	}
}

// splitQENGTokens splits a QENG record into unquoted comma fields. Quoted
// sections that contain commas are kept verbatim as single tokens.
func splitQENGTokens(response string) []string {
	line := response
	if index := strings.Index(line, "+QENG:"); index >= 0 {
		line = line[index+len("+QENG:"):]
	}
	line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "OK"))
	tokens := []string{}
	current := strings.Builder{}
	inQuotes := false
	for _, character := range line {
		switch {
		case character == '"':
			inQuotes = !inQuotes
		case character == ',' && !inQuotes:
			tokens = append(tokens, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(character)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		tokens = append(tokens, strings.TrimSpace(current.String()))
	}
	return tokens
}

// parseServingCell understands the positional modern format:
// +QENG: "servingcell","LIMSRV","LTE","FDD",460,01,C2FCC1F,207,1650,3,5,5,E624,-102,-11,-71,4,25
// with fields state, rat, duplex, mcc, mnc, cellid(hex), pcid, earfcn,
// freq_band, ul_bw, dl_bw, tac(hex), rsrp, rsrq, rssi, sinr, srxlev.
func parseServingCell(response string) *domain.CellularServingCell {
	tokens := splitQENGTokens(response)
	for index, token := range tokens {
		if index == 0 && !strings.EqualFold(token, "servingcell") {
			return nil
		}
	}
	if len(tokens) < 10 {
		return nil
	}
	cell := &domain.CellularServingCell{
		State:  tokenOrEmpty(tokens, 1),
		RAT:    tokenOrEmpty(tokens, 2),
		Duplex: tokenOrEmpty(tokens, 3),
	}
	mcc := tokenOrEmpty(tokens, 4)
	mnc := tokenOrEmpty(tokens, 5)
	if mcc != "" && mnc != "" {
		cell.PLMN = mcc + mnc
	}
	cell.CellIDHex = strings.ToUpper(tokenOrEmpty(tokens, 6))
	cell.PCI = parseUint16(tokenOrEmpty(tokens, 7))
	earfcn, _ := strconv.ParseUint(tokenOrEmpty(tokens, 8), 10, 32)
	cell.EARFCN = uint32(earfcn)
	cell.Band = normaliseBand(tokenOrEmpty(tokens, 9))
	cell.UplinkBandwidthMHz = lteBandwidthMHz(tokenOrEmpty(tokens, 10))
	cell.DownlinkBandwidthMHz = lteBandwidthMHz(tokenOrEmpty(tokens, 11))
	cell.TACHex = strings.ToUpper(tokenOrEmpty(tokens, 12))
	cell.RSRP = parseIntOrZero(tokenOrEmpty(tokens, 13))
	cell.RSRQ = parseIntOrZero(tokenOrEmpty(tokens, 14))
	cell.RSSI = parseIntOrZero(tokenOrEmpty(tokens, 15))
	cell.SINR = parseIntOrZero(tokenOrEmpty(tokens, 16))
	if cell.EARFCN >= 600000 {
		cell.FrequencyMHz = nrDownlinkFrequencyMHz(cell.EARFCN)
	} else {
		cell.FrequencyMHz = lteDownlinkFrequencyMHz(cell.EARFCN)
	}
	if cell.State == "" || cell.RAT == "" {
		return nil
	}
	return cell
}

func tokenOrEmpty(tokens []string, index int) string {
	if index < 0 || index >= len(tokens) {
		return ""
	}
	return strings.TrimSpace(tokens[index])
}

func parseUint16(value string) uint16 {
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(parsed)
}

func parseIntOrZero(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

// normaliseBand renders "3" as "B3", keeps "n78"/"B3" style input as-is.
func normaliseBand(value string) string {
	if value == "" {
		return ""
	}
	if value[0] == 'n' || value[0] == 'N' {
		return "n" + value[1:]
	}
	if value[0] == 'B' || value[0] == 'b' {
		return "B" + value[1:]
	}
	return "B" + value
}

type cellularCache struct {
	mu        sync.Mutex
	value     *domain.CellularServingCell
	readAt    time.Time
	inFlight  bool
	succeeded bool
}

func (p *Provider) cachedCellular(ctx context.Context) *domain.CellularServingCell {
	p.cellular.mu.Lock()
	if p.cellular.inFlight || (p.cellular.succeeded && time.Since(p.cellular.readAt) < cellularCacheTTL) {
		value := p.cellular.value
		p.cellular.mu.Unlock()
		return value
	}
	p.cellular.inFlight = true
	p.cellular.mu.Unlock()

	value := p.queryServingCell(ctx)

	p.cellular.mu.Lock()
	p.cellular.inFlight = false
	p.cellular.readAt = time.Now()
	if value != nil {
		p.cellular.value = value
		p.cellular.succeeded = true
	}
	p.cellular.mu.Unlock()
	return value
}

func (p *Provider) queryServingCell(ctx context.Context) *domain.CellularServingCell {
	parsed, err := p.snapshotContent(ctx, "system_metrics")
	if err != nil {
		return nil
	}
	for index := range parsed.Lines {
		line := parsed.Lines[index]
		if !line.Capabilities.ModemInterface {
			continue
		}
		path, found := parsed.LinePaths[line.ID]
		if !found || !path.IsValid() {
			continue
		}
		response, err := p.commandATPath(ctx, path, "system_metrics", cellularServingQuery)
		if err != nil {
			continue
		}
		return parseServingCell(response)
	}
	return nil
}
