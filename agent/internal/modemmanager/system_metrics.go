package modemmanager

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// SystemMetrics observes the QDC507 module's own health (CPU load, memory,
// flash usage, temperatures, process counts) over the module ADB shell, plus
// the 4G data-port counters on the host and the serving cell over the
// ModemManager AT passthrough. Everything is best-effort: individual
// collectors that fail are simply omitted from the result so the dashboard
// can render what is known.
//
// The temperature query and the module shell probe each cost a round trip,
// so both are cached briefly.

const (
	systemMetricsTempTTL = 30 * time.Second
	modemTempCommand     = "AT+QTEMP"
	modemTempNumberLimit = 110
)

type modemTempCache struct {
	mu        sync.Mutex
	value     *float64
	readAt    time.Time
	inFlight  bool
	succeeded bool
}

var numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

func (p *Provider) SystemMetrics(ctx context.Context) (domain.SystemMetrics, error) {
	metrics := domain.SystemMetrics{ObservedAt: time.Now().UTC()}
	p.collectModuleDetails(ctx, &metrics)
	metrics.Interfaces = p.collectNetworkCounters()
	metrics.ModemTempCelsius = p.cachedModemTemperature(ctx)
	metrics.Cellular = p.cachedCellular(ctx)
	if metrics.Interfaces == nil {
		metrics.Interfaces = []domain.NetworkInterfaceCounters{}
	}
	return metrics, nil
}

func percentOfCapacity(load float64, cores int) float64 {
	if cores <= 0 {
		return 0
	}
	return load / float64(cores) * 100
}

func (p *Provider) collectNetworkCounters() []domain.NetworkInterfaceCounters {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	interfaces := make([]domain.NetworkInterfaceCounters, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "lo" {
			continue
		}
		rx, rxErr := readCounterFile("/sys/class/net", name, "rx_bytes")
		tx, txErr := readCounterFile("/sys/class/net", name, "tx_bytes")
		if rxErr != nil || txErr != nil {
			continue
		}
		// Report cumulative totals that survive interface recreation instead
		// of raw kernel counters (which reset on every mobile-data toggle).
		rxTotal, txTotal := p.traffic.apply(name, rx, tx)
		interfaces = append(interfaces, domain.NetworkInterfaceCounters{
			Name:    name,
			RxBytes: rxTotal,
			TxBytes: txTotal,
		})
	}
	sort.Slice(interfaces, func(left, right int) bool {
		return interfaces[left].Name < interfaces[right].Name
	})
	return interfaces
}

func readCounterFile(base, name, counter string) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("%s/%s/statistics/%s", base, name, counter))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

// cachedModemTemperature returns the cached module temperature when fresh;
// otherwise it queries AT+QTEMP over the ModemManager passthrough once. A
// failed query is retried on the next call but never blocks longer than the
// command timeout.
func (p *Provider) cachedModemTemperature(ctx context.Context) *float64 {
	p.modemTemp.mu.Lock()
	if p.modemTemp.inFlight || (p.modemTemp.succeeded && time.Since(p.modemTemp.readAt) < systemMetricsTempTTL) {
		value := p.modemTemp.value
		p.modemTemp.mu.Unlock()
		return value
	}
	p.modemTemp.inFlight = true
	p.modemTemp.mu.Unlock()

	value := p.queryModemTemperature(ctx)

	p.modemTemp.mu.Lock()
	p.modemTemp.inFlight = false
	p.modemTemp.readAt = time.Now()
	if value != nil {
		p.modemTemp.value = value
		p.modemTemp.succeeded = true
	}
	p.modemTemp.mu.Unlock()
	return value
}

func (p *Provider) queryModemTemperature(ctx context.Context) *float64 {
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
		response, err := p.commandATPath(ctx, path, "system_metrics", modemTempCommand)
		if err != nil {
			continue
		}
		return parseModemTemperature(response)
	}
	return nil
}

// parseModemTemperature takes the highest plausible Celsius value from a
// QTEMP-style response (for example: +QTEMP: "cpu_die_tem",41 / "pa_tem",38).
func parseModemTemperature(response string) *float64 {
	best := 0.0
	found := false
	for _, match := range numberPattern.FindAllString(response, -1) {
		value, err := strconv.ParseFloat(match, 64)
		if err != nil || value <= 0 || value > modemTempNumberLimit {
			continue
		}
		if !found || value > best {
			best = value
			found = true
		}
	}
	if !found {
		return nil
	}
	return &best
}
