// nknpeer is a small cross-platform NKN test peer used to exercise the
// ModemDeck NKN SMS-forwarding channel end to end. Direct address-to-address
// sends need no topic subscription, so no on-chain fees are involved.
//
// Usage:
//
//	nknpeer gen                          print a fresh 32B seed (hex) + client address
//	nknpeer address -seed <seed>         print the derived client address (no network)
//	nknpeer receive -seed <seed>         connect and print every received message
//	nknpeer send -seed <seed> -dest <addr> -msg <text>
//
// Seed format: 64-char hex (optional 0x prefix) or base64, 32 bytes — same
// rule the gateway applies to the variable named in the forwarder's
// credential_ref (e.g. HOMESIM_NKN_SEED).
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

// makeAddressString mirrors github.com/nknorg/nkn/v2/util/address so the
// printed address is byte-for-byte what the SDK itself would derive.
func makeAddressString(pubKey []byte, identifier string) string {
	if identifier != "" {
		return identifier + "." + hex.EncodeToString(pubKey)
	}
	return hex.EncodeToString(pubKey)
}

const (
	peerIdentifier    = "modemdeck-peer"
	connectTimeout    = 30 * time.Second
	maxHoldingSeconds = 3600
	numSubClients     = 2
)

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "nknpeer: "+format+"\n", args...)
	os.Exit(1)
}

// parseSeed accepts 64-char hex or base64, mirroring the gateway resolver.
func parseSeed(raw string) []byte {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "0x"))
	if raw == "" {
		die("seed is required (32-byte hex or base64)")
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded
	}
	die("seed must be a 32-byte value as hex or base64")
	return nil
}

func accountAddress(seed []byte, identifier string) string {
	account, err := nkn.NewAccount(seed)
	if err != nil {
		die("create account: %v", err)
	}
	return makeAddressString(account.PubKey(), identifier)
}

func newClient(seed []byte, identifier, rpcAddrs string) *nkn.MultiClient {
	account, err := nkn.NewAccount(seed)
	if err != nil {
		die("create account: %v", err)
	}
	var config *nkn.ClientConfig
	if addrs := strings.Split(rpcAddrs, ","); strings.TrimSpace(rpcAddrs) != "" {
		clean := make([]string, 0, len(addrs))
		for _, a := range addrs {
			if a = strings.TrimSpace(a); a != "" {
				clean = append(clean, a)
			}
		}
		if len(clean) > 0 {
			config = &nkn.ClientConfig{SeedRPCServerAddr: nkn.NewStringArray(clean...)}
		}
	}
	client, err := nkn.NewMultiClient(account, identifier, numSubClients, false, config)
	if err != nil {
		die("create client: %v", err)
	}
	select {
	case <-client.OnConnect.C:
	case <-time.After(connectTimeout):
		client.Close()
		die("NKN connect timed out after %s", connectTimeout)
	}
	return client
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, `usage:
  nknpeer gen
  nknpeer address -seed <seed>
  nknpeer receive -seed <seed> [-identifier <id>] [-timeout <dur>]
  nknpeer send -seed <seed> -dest <addr> [-msg <text>] [-identifier <id>]
  nknpeer subscribe -seed <seed> -topic <topic> [-duration <blocks>] [-meta <text>] [-identifier <id>]
  nknpeer subscribers -topic <topic> [-rpc <addrs>]
`)
		os.Exit(2)
	}
	subcommand := os.Args[1]
	fs := flag.NewFlagSet(subcommand, flag.ExitOnError)
	var seedRaw, dest, msg, identifier, timeoutRaw, rpcAddr, topic, meta, durationRaw string
	fs.StringVar(&seedRaw, "seed", "", "32-byte secret seed (hex or base64)")
	fs.StringVar(&dest, "dest", "", "destination NKN client address")
	fs.StringVar(&msg, "msg", "nknpeer test message", "message text to send")
	fs.StringVar(&identifier, "identifier", peerIdentifier, "client identifier")
	fs.StringVar(&timeoutRaw, "timeout", "60s", "receive/send wait budget")
	fs.StringVar(&rpcAddr, "rpc", "", "comma-separated seed RPC addresses (e.g. http://192.168.120.190:30003) to prefer a self-hosted node")
	fs.StringVar(&topic, "topic", "", "topic name for subscribe/subscribers")
	fs.StringVar(&meta, "meta", "", "public meta string attached to the subscription")
	fs.StringVar(&durationRaw, "duration", "4320", "subscription duration in blocks (~24h at 20s blocks)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		die("parse flags: %v", err)
	}

	timeout, err := time.ParseDuration(timeoutRaw)
	if err != nil {
		die("bad -timeout: %v", err)
	}

	switch subcommand {
	case "gen":
		seed := make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			die("random seed: %v", err)
		}
		out, _ := json.MarshalIndent(map[string]string{
			"seed_hex": hex.EncodeToString(seed),
			"env_line": fmt.Sprintf("HOMESIM_NKN_SEED=%s", hex.EncodeToString(seed)),
			"address":  accountAddress(seed, identifier),
		}, "", "  ")
		fmt.Println(string(out))

	case "address":
		fmt.Println(accountAddress(parseSeed(seedRaw), identifier))

	case "receive":
		if seedRaw == "" {
			die("receive requires -seed")
		}
		seed := parseSeed(seedRaw)
		client := newClient(seed, identifier, rpcAddr)
		defer client.Close()
		fmt.Printf("NKNPEER_ADDRESS %s\n", client.Address())
		fmt.Println("listening for messages; Ctrl+C to quit")
		done := make(chan os.Signal, 1)
		signal.Notify(done, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(done)
		window := time.NewTimer(timeout)
		defer window.Stop()
		for {
			select {
			case m, ok := <-client.OnMessage.C:
				if !ok {
					return
				}
				ackErr := m.Reply([]byte("nknpeer-ack"))
				out, _ := json.Marshal(map[string]any{
					"from":      m.Src,
					"encrypted": m.Encrypted,
					"data":      string(m.Data),
					"acked":     ackErr == nil,
				})
				fmt.Printf("NKNPEER_MESSAGE %s\n", out)
			case <-done:
				fmt.Println("bye")
				return
			case <-window.C:
				fmt.Println("receive window elapsed, exiting")
				return
			}
		}

	case "send":
		if seedRaw == "" || dest == "" {
			die("send requires -seed and -dest")
		}
		seed := parseSeed(seedRaw)
		client := newClient(seed, identifier, rpcAddr)
		defer client.Close()
		// NoReply:false — wait for the receiver's ack so a success line here
		// is real end-to-end proof, not just a handoff to the network.
		config := &nkn.MessageConfig{NoReply: false, MaxHoldingSeconds: maxHoldingSeconds}
		started := time.Now()
		onReply, err := client.Send(nkn.NewStringArray(dest), []byte(msg), config)
		if err != nil {
			die("send: %v", err)
		}
		select {
		case reply := <-onReply.C:
			out, _ := json.Marshal(map[string]any{
				"to":         dest,
				"from":       reply.Src,
				"encrypted":  reply.Encrypted,
				"reply":      string(reply.Data),
				"message":    msg,
				"elapsed_ms": time.Since(started).Milliseconds(),
			})
			fmt.Printf("NKNPEER_SENT %s\n", out)
		case <-time.After(timeout):
			die("no reply within %s (receiver offline or network unreachable)", timeout)
		}

	case "subscribe":
		if seedRaw == "" || topic == "" {
			die("subscribe requires -seed and -topic")
		}
		duration, err := strconv.Atoi(durationRaw)
		if err != nil || duration <= 0 {
			die("bad -duration %q: must be a positive block count", durationRaw)
		}
		seed := parseSeed(seedRaw)
		client := newClient(seed, identifier, rpcAddr)
		defer client.Close()
		// Fee defaults to 0 (TransactionConfig nil) — a subscription costs
		// nothing beyond the on-chain record itself.
		hash, err := client.Subscribe(identifier, topic, duration, meta, nil)
		if err != nil {
			die("subscribe: %v", err)
		}
		out, _ := json.MarshalIndent(map[string]any{
			"tx_hash":  hash,
			"topic":    topic,
			"address":  client.Address(),
			"duration": duration,
			"meta":     meta,
			"fee":      "0",
		}, "", "  ")
		fmt.Printf("NKNPEER_SUBSCRIBED %s\n", out)

	case "subscribers":
		if topic == "" {
			die("subscribers requires -topic")
		}
		config := &nkn.RPCConfig{RPCTimeout: 10000, RPCConcurrency: 2}
		if addrs := strings.Split(rpcAddr, ","); strings.TrimSpace(rpcAddr) != "" {
			clean := make([]string, 0, len(addrs))
			for _, a := range addrs {
				if a = strings.TrimSpace(a); a != "" {
					clean = append(clean, a)
				}
			}
			if len(clean) > 0 {
				config.SeedRPCServerAddr = nkn.NewStringArray(clean...)
			}
		}
		result, err := nkn.GetSubscribers(topic, 0, 0, true, false, nil, config)
		if err != nil {
			die("getsubscribers: %v", err)
		}
		list := map[string]string{}
		if result != nil && result.Subscribers != nil {
			list = result.Subscribers.Map()
		}
		out, _ := json.MarshalIndent(map[string]any{
			"topic":       topic,
			"count":       len(list),
			"subscribers": list,
		}, "", "  ")
		fmt.Println(string(out))

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", subcommand)
		os.Exit(2)
	}
}
