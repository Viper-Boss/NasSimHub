package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/communication"
	"github.com/human-agent65535/modemdeck/internal/httpapi"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// NasSimHub Node settings. All optional.
//
//	MODEMDECK_NASSIMHUB_NODES=off        disable Node support entirely
//	MODEMDECK_NASSIMHUB_USB_URL=...      override https://10.55.0.2:7580
//	MODEMDECK_NASSIMHUB_POLL_SECONDS=10  poll interval (3..300)
const (
	nassimhubDirectoryName     = "nassimhub"
	defaultNodePollInterval    = 10 * time.Second
	nodeTriggeredRefreshBudget = 30 * time.Second
)

type nassimhubNodes struct {
	fleet    *nassimhubnode.Fleet
	interval time.Duration
	refresh  chan struct{}
}

func startNasSimHubNodes(
	ctx context.Context,
	logger *slog.Logger,
	dataDirectory string,
	repository *store.Store,
	communications *communication.Service,
	coreVersion string,
) (*nassimhubNodes, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MODEMDECK_NASSIMHUB_NODES")), "off") {
		logger.Info("NasSimHub Node support disabled", "component", "nassimhub")
		return nil, nil
	}
	identity, err := nassimhubnode.LoadOrCreateIdentity(filepath.Join(dataDirectory, nassimhubDirectoryName))
	if err != nil {
		return nil, fmt.Errorf("load NasSimHub core identity: %w", err)
	}
	fleet, err := nassimhubnode.New(ctx, nassimhubnode.Options{
		Identity:    identity,
		Repository:  repository,
		CoreVersion: nodeCoreVersion(coreVersion),
		USBBaseURL:  os.Getenv("MODEMDECK_NASSIMHUB_USB_URL"),
		Logger:      logger.With("component", "nassimhub"),
	})
	if err != nil {
		return nil, fmt.Errorf("open NasSimHub nodes: %w", err)
	}
	if err := communications.SetNodeSource(fleet); err != nil {
		return nil, err
	}
	nodes := &nassimhubNodes{
		fleet:    fleet,
		interval: nodePollInterval(),
		refresh:  make(chan struct{}, 1),
	}
	// A Node change triggers one hardware refresh. Changes arriving while a
	// refresh is pending coalesce into it, so a burst of polls cannot queue a
	// burst of snapshot commits.
	fleet.OnChange(func() {
		select {
		case nodes.refresh <- struct{}{}:
		default:
		}
	})
	logger.Info("NasSimHub Node support enabled", "component", "nassimhub", "core_id", identity.CoreID)
	return nodes, nil
}

func (n *nassimhubNodes) run(ctx context.Context, communications *communication.Service, logger *slog.Logger) {
	if n == nil {
		return
	}
	go n.fleet.Run(ctx, n.interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.refresh:
			refreshCtx, cancel := context.WithTimeout(ctx, nodeTriggeredRefreshBudget)
			if _, err := communications.Refresh(refreshCtx); err != nil {
				logger.Warn("node-triggered refresh failed", "component", "nassimhub", "error", err)
			}
			cancel()
		}
	}
}

func (n *nassimhubNodes) service() httpapi.NasSimHubNodeService {
	if n == nil {
		return nil
	}
	return n.fleet
}

func nodePollInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("MODEMDECK_NASSIMHUB_POLL_SECONDS"))
	if raw == "" {
		return defaultNodePollInterval
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 3 || seconds > 300 {
		return defaultNodePollInterval
	}
	return time.Duration(seconds) * time.Second
}

// nodeCoreVersion is the version a Node's min_core_version is compared with.
//
// The protocol exempts a development Core ("" or "dev") from that check, and
// treats any other version it cannot parse as fatal - the Node's minimum is a
// demand that cannot be assumed met. Local NAS builds carry labels such as
// "fnos-local-0.1", which are not versions at all; passing one through would
// make every pairing fail with "version_unreadable". Such a label is reported
// as a development build instead. The protocol major/minor check still applies.
func nodeCoreVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" || version == "dev" {
		return ""
	}
	if _, err := proto.ParseVersion(version); err != nil {
		return ""
	}
	return version
}
