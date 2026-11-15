package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
	"github.com/nknorg/nkngomobile"
)

// NKN outbound channel for HomeSIM SMS forwarding. The NAS client keeps a
// long-lived NKN identity derived from a 32-byte secret seed resolved from an
// environment variable (same rule as ServerChan/PushPlus credentials). Events
// fan out either directly to configured NKN addresses or to every subscriber
// of a topic; messages to offline receivers are held by the network for up to
// nknMaxHoldingSeconds.

const (
	nknBaseIdentifier    = "modemdeck"
	nknConnectTimeout    = 30 * time.Second
	nknWaitExtra         = 5 * time.Second
	nknMaxHoldingSeconds = 3600
	nknTestTimeout       = 45 * time.Second
	// nknSeedRPCEnv optionally names environment variables holding seed RPC
	// addresses (comma separated) that the gateway's own NKN client should
	// contact, e.g. HOMESIM_NKN_SEED_RPC=http://127.0.0.1:30003 for a
	// self-hosted node on the same host. The seed list sent to the SDK is:
	// built-in node (while running) first, then this environment variable,
	// then the official seeds appended as fallback so a dead local node never
	// blocks delivery.
	nknSeedRPCEnv = "HOMESIM_NKN_SEED_RPC"
)

// nknOfficialSeedRPC mirrors the nkn-sdk-go DefaultSeedRPCServerAddr list
// (seed.nkn.org + mainnet-seed-0001..0008). More candidates mean the gateway
// client fails over faster when a particular seed RPC is slow or unreachable.
var nknOfficialSeedRPC = []string{
	"http://seed.nkn.org:30003",
	"http://mainnet-seed-0001.nkn.org:30003",
	"http://mainnet-seed-0002.nkn.org:30003",
	"http://mainnet-seed-0003.nkn.org:30003",
	"http://mainnet-seed-0004.nkn.org:30003",
	"http://mainnet-seed-0005.nkn.org:30003",
	"http://mainnet-seed-0006.nkn.org:30003",
	"http://mainnet-seed-0007.nkn.org:30003",
	"http://mainnet-seed-0008.nkn.org:30003",
}

// nknCommunitySeedRPC lists community-operated seed RPC servers physically in
// mainland China. The official 44 seeds all resolve overseas (verified
// 2026-09-08: 8 unique IPs in SG/US/NL/GB/DE/CA/IN), so a domestic seed cuts
// first-connect latency for China deployments. Entries are probed for a
// working JSON-RPC on 30003 before being listed. Community IPs drift —
// re-run dev/collect_nkn_cn_nodes.py to refresh before editing.
var nknCommunitySeedRPC = []string{
	"http://183.53.109.45:30003", // Chinanet, ~95ms from a Shanghai LAN, verified 2026-09-08
}

// nknSeedRPCList returns seed RPC candidates in preference order: verified
// China community nodes first, then the official list as the durable fallback.
func nknSeedRPCList() []string {
	list := make([]string, 0, len(nknCommunitySeedRPC)+len(nknOfficialSeedRPC))
	list = append(list, nknCommunitySeedRPC...)
	list = append(list, nknOfficialSeedRPC...)
	return list
}

type homeSIMNKNClient struct {
	client  *nkn.MultiClient
	ready   chan struct{}
	err     error
	address string
}

func nknClientCacheKey(seed []byte) string {
	sum := sha256.Sum256(seed)
	return hex.EncodeToString(sum[:8])
}

// homeSIMNKNClient returns a connected NKN multiclient for the seed, creating
// and caching one per seed. Failed entries are dropped so the next call
// retries.
func (api *API) homeSIMNKNClient(seed []byte) (*homeSIMNKNClient, error) {
	key := nknClientCacheKey(seed)
	api.homeSIMNKNMu.Lock()
	entry, ok := api.homeSIMNKNClients[key]
	if !ok {
		entry = &homeSIMNKNClient{ready: make(chan struct{})}
		if api.homeSIMNKNClients == nil {
			api.homeSIMNKNClients = make(map[string]*homeSIMNKNClient)
		}
		api.homeSIMNKNClients[key] = entry
		go func(entry *homeSIMNKNClient, seed []byte) {
			account, err := nkn.NewAccount(seed)
			if err == nil {
				// Seed preference: built-in node (while running), then the
				// operator's env override, then verified China community
				// seeds, with the official seeds always appended as fallback
				// so a dead local or community node never blocks delivery.
				var addrs []string
				if builtin := api.nknNode.seedRPCAddress(); builtin != "" {
					addrs = append(addrs, builtin)
				}
				addrs = append(addrs, nknAddressList(os.Getenv(nknSeedRPCEnv))...)
				addrs = append(addrs, nknSeedRPCList()...)
				clientConfig := &nkn.ClientConfig{
					SeedRPCServerAddr: nkngomobile.NewStringArray(addrs...),
				}
				var multi *nkn.MultiClient
				multi, err = nkn.NewMultiClient(account, nknBaseIdentifier, 2, false, clientConfig)
				if err == nil {
					entry.client = multi
					entry.address = multi.Address()
					select {
					case <-multi.OnConnect.C:
					case <-time.After(nknConnectTimeout):
						_ = multi.Close()
						err = fmt.Errorf("NKN network connect timed out after %s", nknConnectTimeout)
					}
				}
			}
			entry.err = err
			close(entry.ready)
		}(entry, append([]byte(nil), seed...))
	}
	api.homeSIMNKNMu.Unlock()

	select {
	case <-entry.ready:
	case <-time.After(nknConnectTimeout + nknWaitExtra):
		return nil, fmt.Errorf("NKN client is still connecting to the network; try again shortly")
	}
	if entry.err != nil || entry.client == nil || entry.client.IsClosed() {
		api.homeSIMNKNMu.Lock()
		if api.homeSIMNKNClients[key] == entry {
			delete(api.homeSIMNKNClients, key)
		}
		api.homeSIMNKNMu.Unlock()
		if entry.err != nil {
			return nil, entry.err
		}
		return nil, fmt.Errorf("NKN client connection was closed; retry")
	}
	return entry, nil
}

// resolveNKNSeed loads the 32-byte secret seed named by an environment
// variable. The value may be 64-char hex or base64.
func resolveNKNSeed(reference string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(reference))
	if raw == "" {
		return nil, fmt.Errorf("environment variable %s is not set on the server", reference)
	}
	if decoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x")); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("environment variable %s must hold a 32-byte NKN seed as hex or base64", reference)
}

func nknAddressList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	addresses := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			addresses = append(addresses, field)
		}
	}
	return addresses
}
