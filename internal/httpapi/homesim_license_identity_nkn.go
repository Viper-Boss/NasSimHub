package httpapi

import (
	"context"
	"encoding/json"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

// Answering an issuer's identity request over NKN.
//
// This is the FIRST HALF of the licensing conversation; receiveLicenseDelivery
// next door is the second. Without this half the second one cannot be reached
// by an issuer who does not already know the customer's hardware: a
// certificate binds a machine ID or a module IMEI, and until this existed the
// only way for the issuer to learn those was to ask the customer to read them
// off a settings page and type them into a chat window. Typed identifiers
// arrive transposed, and a transposed IMEI produces a certificate that is
// perfectly valid and unlocks nothing, which is the worst failure this system
// can have because it looks identical to a successful purchase from the
// issuing side.
//
// So the issuing console asks the gateway directly, and the gateway answers
// with exactly the two facts a certificate has to name. The console then signs
// against the values it received rather than the values somebody typed.
//
// THE NONCE IS THE CONSOLE'S PROTECTION, NOT OURS. It is echoed verbatim so
// the console can tell this answer from a replayed older one; nothing here
// depends on it. This handler is deliberately stateless -- it holds no pending
// requests, so a flood of them costs a JSON parse each and nothing that
// accumulates.
//
// WHAT THIS DISCLOSES, stated plainly because it is the one real cost. Anyone
// who knows this gateway's NKN address can ask and receive the machine ID and
// the module IMEIs. Neither is a secret that protects anything: the machine ID
// is a hash of the board's storage fingerprint, and the IMEI is printed on the
// module and shown on the module card behind the login. Knowing them lets
// somebody ASK for a certificate; it does not let them make one, because that
// takes the issuer's private key. The reply carries nothing else -- no
// subscriber data, no numbers, no locations, no keys -- and the NKN address
// should still be handed out like the semi-private fact it is.

// licenseIdentityRequest is what the issuing console sends.
type licenseIdentityRequest struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	// Nonce is echoed verbatim in the reply so the console can match the
	// answer to its own question and refuse a replayed one.
	Nonce string `json:"nonce"`
}

// licenseIdentityReply is what this gateway sends back.
type licenseIdentityReply struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	Nonce     string `json:"nonce"`
	MachineID string `json:"machine_id,omitempty"`
	// MachineError explains an empty machine ID. A board whose fingerprint
	// cannot be read cannot be issued a machine certificate at all, and the
	// operator on the other end needs to see WHY rather than a blank field --
	// a blank field reads as "the gateway is broken" and sends them looking in
	// the wrong place.
	MachineError string   `json:"machine_error,omitempty"`
	ModuleSNs    []string `json:"module_sns"`
	ReportedAt   int64    `json:"reported_at"`
}

const (
	licenseIdentityType    = "homesim.license.identity"
	licenseIdentityVersion = 1
	// The request is three short strings. Anything bigger is not ours, and
	// refusing it before json.Unmarshal keeps a large message from costing
	// anything to reject.
	licenseIdentityMaxRequestBytes = 1024
	// The reply is one machine ID and a handful of IMEIs. Same reasoning, in
	// the other direction: a reply that somehow grew past this is a bug here,
	// and sending it would be worse than sending nothing.
	licenseIdentityMaxReplyBytes = 2048
)

// parseLicenseIdentityRequest decides whether a message is one of ours.
//
// Separate from the handler, and tested on its own, because this decision is
// shared property: the dispatch loop offers each message to the receivers in
// turn and stops at the first one that claims it, so a filter that is too
// loose here swallows chat and DHT traffic, and one that is too tight makes
// the gateway look unreachable to the issuer. Neither failure announces
// itself.
//
// The nonce bounds are part of the filter rather than a separate check: a
// message with a missing or absurd nonce is not a request this protocol can
// answer, and claiming it in order to discard it would hide it from a future
// handler that might understand it.
func parseLicenseIdentityRequest(data []byte) (licenseIdentityRequest, bool) {
	var request licenseIdentityRequest
	if len(data) == 0 || len(data) > licenseIdentityMaxRequestBytes {
		return request, false
	}
	if json.Unmarshal(data, &request) != nil ||
		request.Type != licenseIdentityType ||
		request.Version != licenseIdentityVersion ||
		len(request.Nonce) < 8 || len(request.Nonce) > 64 {
		return licenseIdentityRequest{}, false
	}
	return request, true
}

// receiveLicenseIdentity answers one identity request.
//
// Returns true when the message was ours, so the shared dispatch loop stops
// offering it around -- the same contract as every receiver beside this one. A
// request that is ours but unusable still returns true: it was addressed to
// this handler and no other handler will want it.
func (api *API) receiveLicenseIdentity(client *homeSIMNKNClient, message *nkn.Message) bool {
	if client == nil || client.client == nil || message == nil {
		return false
	}
	request, ok := parseLicenseIdentityRequest(message.Data)
	if !ok {
		return false
	}

	// The reply goes back to the asker and only the asker: NKN messages are
	// end-to-end encrypted between the two endpoints, so the identity travels
	// a channel nobody in between reads.
	reply := licenseIdentityReply{
		Type:       licenseIdentityType,
		Version:    licenseIdentityVersion,
		Nonce:      request.Nonce,
		ModuleSNs:  api.moduleSerials(context.Background()),
		ReportedAt: time.Now().Unix(),
	}
	if runtime := api.licensing(); runtime != nil {
		reply.MachineID, reply.MachineError = runtime.machineID()
	} else {
		// Fixture mode and a gateway whose data directory is not ready both
		// land here. Saying so beats an empty machine ID with no explanation.
		reply.MachineError = "存储未就绪，无法读取机器指纹"
	}

	payload, err := json.Marshal(reply)
	if err != nil || len(payload) > licenseIdentityMaxReplyBytes {
		// Marshalling plain strings cannot realistically fail; the bound is
		// belt and braces. Either way silence beats a truncated reply, and the
		// console's own timeout turns silence into a clear message.
		api.logger.Warn("a licence identity reply could not be built",
			"to", message.Src,
			"error", err,
		)
		return true
	}
	if _, err := client.client.Send(
		nkn.NewStringArray(message.Src),
		payload,
		&nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 120},
	); err != nil {
		// The console retries with a fresh nonce. This line is what makes the
		// failure visible on this side when a customer reports that the issuer
		// "never got an answer".
		api.logger.Warn("a licence identity reply could not be sent",
			"to", message.Src,
			"error", err,
		)
	}
	return true
}
