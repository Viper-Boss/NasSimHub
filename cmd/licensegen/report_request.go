package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/clientreport"
)

// reportRequest signs a request for one gateway's client report.
//
// The output is the JSON to send to that gateway's NKN address. The gateway
// answers only a request signed by the issuer key and addressed to its own
// machine ID, so this is the step that turns "anyone can ask" into "only we
// can ask" -- and the reason the report may carry versions, entitlements and
// the anti-fraud counters at all.
//
// The machine ID comes from the unauthenticated identity request, which is the
// step that already existed: ask who the gateway is, then sign a report request
// for that answer.
//
// THE REQUEST EXPIRES. clientreport.FreshnessWindow is five minutes either
// side of the gateway's clock, so a request signed and left in a file overnight
// will be refused. That is deliberate -- it is what bounds replay of a captured
// request -- so sign one when you are about to send it, not in advance.
func reportRequest(arguments []string) error {
	set := flag.NewFlagSet("report-request", flag.ExitOnError)
	keyPath := set.String("key", "", "issuer private key file (hex), as written by keygen")
	machineID := set.String("machine", "", "the gateway's machine ID, from its identity reply")
	nonce := set.String("nonce", "", "optional; a random one is generated when empty")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*machineID) == "" {
		return fmt.Errorf("-machine is required: ask the gateway for its identity first, " +
			"then sign a report request for the machine ID it reports")
	}

	key, err := loadPrivateKey(*keyPath)
	if err != nil {
		return err
	}

	value := strings.TrimSpace(*nonce)
	if value == "" {
		// Generated rather than defaulted, because a fixed nonce would let the
		// gateway's answer to one request be replayed as the answer to another
		// -- the console matches replies to questions by this value.
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generate nonce: %w", err)
		}
		value = hex.EncodeToString(raw)
	}

	request, err := clientreport.Sign(value, strings.TrimSpace(*machineID), time.Now(), key)
	if err != nil {
		return err
	}
	document, err := json.Marshal(request)
	if err != nil {
		return err
	}
	fmt.Println(string(document))

	public, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("signing key has no public half")
	}
	fmt.Fprintf(os.Stderr,
		"Send the line above to the gateway's NKN address.\n"+
			"Valid for %s either side of the gateway's clock; sign a fresh one if it is refused.\n"+
			"Signed with the key at %s\n"+
			"Public half: %s\n"+
			"The gateway answers only if its compiled-in key matches that, and its report\n"+
			"echoes the first four bytes so you can check without guessing.\n",
		clientreport.FreshnessWindow, *keyPath,
		base64.StdEncoding.EncodeToString(public))
	return nil
}
