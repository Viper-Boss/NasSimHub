package httpapi

import (
	"encoding/json"
	"time"

	"github.com/human-agent65535/modemdeck/internal/license"
	nkn "github.com/nknorg/nkn-sdk-go"
)

// Delivering a licence certificate over NKN.
//
// The flow the product wants: a customer hands over their NKN address, the
// issuer signs a certificate for their machine ID or module and sends it as a
// message, and the feature unlocks. No web shop, no account, no callback to a
// server that has to stay up for the next ten years.
//
// WHY THIS TRUSTS THE SENDER LESS THAN ITS NEIGHBOURS, which is the thing to
// understand before changing anything here. The DHT and WireGuard
// announcements beside this one both require the sender to be an instance
// already configured as a peer, because those messages ask the receiver to DO
// something -- dial an address, expect an endpoint -- and a stranger asking
// for that is a stranger pointing this gateway somewhere.
//
// A certificate asks for nothing. It is a signed statement, and the signature
// is the whole of its authority: without the issuer's private key nobody can
// produce one, and a certificate for another machine is inert here because it
// names the machine it was issued for. So the sender's identity is not checked
// at all, and that is deliberate rather than an omission -- the issuer is NOT
// a paired peer, and requiring pairing would break the only flow this exists
// to serve while adding no security. What stops abuse is that a message which
// does not verify is discarded, and one that does verify could only have come
// from the issuer.
//
// The one thing a stranger could still do is waste a little CPU on JSON and a
// signature check, which is bounded below and cheaper than the NKN transport
// that delivered it.

// licenseDelivery is the message shape. Typed and versioned like its
// neighbours, so a future format cannot be misread as this one.
type licenseDelivery struct {
	Type    string         `json:"type"`
	Version int            `json:"version"`
	Signed  license.Signed `json:"certificate"`
}

const (
	licenseDeliveryType    = "homesim.license.certificate"
	licenseDeliveryVersion = 1
	// A certificate is a few hundred bytes. The bound stops a large message
	// being used as an amplifier and keeps the parse cheap.
	licenseDeliveryMaxBytes = 4096
)

// receiveLicenseDelivery stores a certificate that arrived over NKN.
//
// Returns true when the message was one of ours, so the dispatch loop stops
// offering it to the other receivers -- the same contract as the DHT
// announcement beside it. A message that is ours but unusable still returns
// true: it was addressed to this handler and no other handler will want it.
func (api *API) receiveLicenseDelivery(message *nkn.Message) bool {
	if message == nil || len(message.Data) > licenseDeliveryMaxBytes {
		return false
	}
	var delivery licenseDelivery
	if json.Unmarshal(message.Data, &delivery) != nil ||
		delivery.Type != licenseDeliveryType ||
		delivery.Version != licenseDeliveryVersion {
		return false
	}

	runtime := api.licensing()
	if runtime == nil {
		// No storage. Logged rather than dropped silently: a customer who was
		// just sent a certificate and saw nothing happen needs this line to
		// exist somewhere.
		api.logger.Warn(
			"a licence certificate arrived but there is nowhere to store it",
			"from", message.Src,
		)
		return true
	}

	// Save verifies before writing, so an unverifiable certificate never
	// reaches the disk. See license.Store.Save.
	if err := runtime.store.Save(delivery.Signed, time.Now()); err != nil {
		api.logger.Warn(
			"a licence certificate was refused",
			"from", message.Src,
			"scope", string(delivery.Signed.Certificate.Scope),
			"certificate", delivery.Signed.Certificate.ID,
			"error", err,
		)
		return true
	}

	// Logged at info, because this is the moment a customer's purchase takes
	// effect and it is the first thing to look for when they say it did not.
	api.logger.Info(
		"a licence certificate was accepted",
		"from", message.Src,
		"scope", string(delivery.Signed.Certificate.Scope),
		"certificate", delivery.Signed.Certificate.ID,
		"features", delivery.Signed.Certificate.Features,
	)
	return true
}
