package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"

	"github.com/human-agent65535/modemdeck/internal/clientreport"
	"github.com/human-agent65535/modemdeck/internal/license"
)

// Answering a signed client-report request over NKN.
//
// This is the step between "who are you" and "here is your certificate". The
// issuing console learns the gateway's identity from receiveLicenseIdentity,
// signs a report request for that specific machine, and gets back what it needs
// to decide WHAT to issue -- and, asked again afterwards, whether the
// certificate it delivered actually took effect.
//
// WHAT IS IN THE REPORT, and why each field is worth disclosing to the issuer:
//
//	the compiled-in issuer key's fingerprint, so the console can tell before
//	signing anything whether this gateway can verify its certificates at all.
//	That failure is otherwise silent and has already cost two debugging
//	sessions in this project.
//
//	the certificates already held, and the ones REJECTED with the reason, so
//	"I paid and nothing unlocked" is answered with expired / wrong machine /
//	module not attached / bad signature instead of a guess.
//
//	each feature's decision, so free allowance is distinguishable from paid.
//
//	the anti-fraud counters, as COUNTS.
//
//	software version, module model and firmware, so a support answer does not
//	begin by asking the customer to read things off a screen.
//
//	the gateway's clock, because certificates expire and a NAS with no NTP
//	can be far enough out to make a valid certificate look expired.
//
// WHAT IS DELIBERATELY NOT IN IT. This is a product that handles other
// people's messages and calls, and the report travels to the vendor. So the
// line is drawn at the hardware:
//
//	no message content, no contacts, no call history;
//	no destination numbers -- the fraud counters are counts, because a count
//	is accountability and a list of numbers is a call log;
//	no ICCID, no IMSI, no subscriber numbers. The IMEI identifies the MODULE
//	and is already disclosed by the identity reply; the ICCID identifies the
//	customer's SIM ACCOUNT, which is a different thing and is not ours;
//	no location, coarse or otherwise;
//	no keys, tokens, passwords, WireGuard material or NKN wallet.
//
// TestTheReportCarriesNoSubscriberData holds that line mechanically, because
// store.Device carries CurrentICCID and a SIM struct right beside the fields
// this does use, and a future "just add the whole device" would be one line.

// processStarted is when this process began, for the uptime figure.
//
// Uptime distinguishes a stable installation from one that is crash-looping,
// which is the difference between "your certificate is fine" and "your gateway
// never stays up long enough to apply it".
var processStarted = time.Now()

// clientReportReply is the signed request's answer.
type clientReportReply struct {
	Type       string `json:"type"`
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	ReportedAt int64  `json:"reported_at"`
	MachineID  string `json:"machine_id"`

	System       clientReportSystem       `json:"system"`
	Modules      []clientReportModule     `json:"modules"`
	Entitlements clientReportEntitlements `json:"entitlements"`
	FraudGuard   clientReportFraudGuard   `json:"fraud_guard"`
}

type clientReportSystem struct {
	AppVersion string `json:"app_version"`
	// IssuerKeyFingerprint is the first four bytes of the compiled-in issuer
	// public key, hex. Four bytes rather than the whole key: it is enough to
	// tell one issuer build from another at a glance, and it is not a value
	// anybody could mistake for something to paste into a config.
	IssuerKeyFingerprint string `json:"issuer_key_fingerprint"`
	// CanVerifyCertificates is false when this build has no usable issuer key,
	// in which case every paid feature is locked and no certificate the console
	// signs will ever help. Worth its own boolean because the state is
	// indistinguishable, from the customer's side, from not having paid.
	CanVerifyCertificates bool `json:"can_verify_certificates"`
	// ClockUnix is the gateway's own clock. The console compares it to its own
	// and can say "your clock is two days slow, that is why it expired".
	ClockUnix     int64 `json:"clock_unix"`
	UptimeSeconds int64 `json:"uptime_seconds"`
}

type clientReportModule struct {
	SN       string `json:"sn"`
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	// Present matters for licensing: a module certificate counts only while its
	// module is attached, so a detached module is a complete explanation for
	// "nothing is unlocked" that costs nothing to report.
	Present  bool   `json:"present"`
	LastSeen string `json:"last_seen,omitempty"`
}

type clientReportEntitlements struct {
	Features     []clientReportFeature     `json:"features"`
	Certificates []clientReportCertificate `json:"certificates"`
	Rejected     []clientReportRejection   `json:"rejected"`
}

type clientReportFeature struct {
	Feature string `json:"feature"`
	Allowed bool   `json:"allowed"`
	// Licensed separates "works because it is free" from "works because they
	// paid", which is the distinction an issuer actually needs.
	Licensed bool     `json:"licensed"`
	Missing  []string `json:"missing,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

type clientReportCertificate struct {
	ID        string   `json:"id"`
	Scope     string   `json:"scope"`
	MachineID string   `json:"machine_id,omitempty"`
	ModuleSN  string   `json:"module_sn,omitempty"`
	Features  []string `json:"features"`
	IssuedAt  int64    `json:"issued_at"`
	ExpiresAt int64    `json:"expires_at"`
}

type clientReportRejection struct {
	ID     string `json:"id"`
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

type clientReportGuard struct {
	Limit     int  `json:"limit"`
	Remaining int  `json:"remaining"`
	Used      int  `json:"used"`
	Unlocked  bool `json:"unlocked"`
	Available bool `json:"available"`
}

type clientReportFraudGuard struct {
	Call    clientReportGuard `json:"call"`
	Message clientReportGuard `json:"message"`
	// Triggered is true when either guard has reached its limit without being
	// unlocked. The single boolean the operator usually wants; the counters
	// beside it are for when they want the detail.
	Triggered bool `json:"triggered"`
}

// The reply is larger than the identity reply but still small: a handful of
// modules, five features, a few certificates.
const clientReportMaxReplyBytes = 16 << 10

// receiveClientReport answers one signed report request.
//
// Returns true when the message was ours, so the shared dispatch loop stops
// offering it around -- the same contract as every receiver beside it.
//
// AN UNAUTHENTICATED OR STALE REQUEST GETS SILENCE, not an error reply. An
// error reply would turn this endpoint into an oracle: a prober could confirm
// which NKN addresses run this software, and by varying machine_id could
// confirm a guessed machine ID without ever holding a key. Silence costs the
// real console nothing, because it retries with a fresh nonce anyway, and the
// refusal is logged locally where the gateway's owner can see it.
func (api *API) receiveClientReport(client *homeSIMNKNClient, message *nkn.Message) bool {
	if client == nil || client.client == nil || message == nil {
		return false
	}
	var request clientreport.Request
	if len(message.Data) == 0 || len(message.Data) > 2048 {
		return false
	}
	if json.Unmarshal(message.Data, &request) != nil ||
		request.Type != clientreport.Type ||
		request.Version != clientreport.Version {
		return false
	}

	runtime := api.licensing()
	machineID := ""
	if runtime != nil {
		machineID, _ = runtime.machineID()
	}
	if err := clientreport.Verify(
		request, machineID, api.issuerPublicKey, time.Now(),
	); err != nil {
		// Logged, not answered. This is the line an owner reads if somebody is
		// probing their gateway, so it names the sender and the reason.
		api.logger.Warn("a client report request was refused",
			"from", message.Src,
			"error", err,
		)
		return true
	}

	reply := api.buildClientReport(context.Background(), request, machineID)
	payload, err := json.Marshal(reply)
	if err != nil || len(payload) > clientReportMaxReplyBytes {
		api.logger.Warn("a client report could not be built",
			"to", message.Src,
			"error", err,
			"bytes", len(payload),
		)
		return true
	}
	if _, err := client.client.Send(
		nkn.NewStringArray(message.Src),
		payload,
		&nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 120},
	); err != nil {
		api.logger.Warn("a client report could not be sent",
			"to", message.Src,
			"error", err,
		)
	}
	return true
}

// buildClientReport assembles the answer.
//
// Separate from the handler and tested directly, because the handler needs a
// live NKN client and this is where every disclosure decision is made.
func (api *API) buildClientReport(
	ctx context.Context,
	request clientreport.Request,
	machineID string,
) clientReportReply {
	now := time.Now()
	reply := clientReportReply{
		Type:       clientreport.Type,
		Version:    clientreport.Version,
		Nonce:      request.Nonce,
		ReportedAt: now.Unix(),
		MachineID:  machineID,
		System: clientReportSystem{
			AppVersion:           api.applicationVersion,
			IssuerKeyFingerprint: issuerKeyFingerprint(api.issuerPublicKey),
			ClockUnix:            now.Unix(),
			UptimeSeconds:        int64(now.Sub(processStarted).Seconds()),
		},
		Modules:      api.clientReportModules(ctx),
		Entitlements: clientReportEntitlements{Features: []clientReportFeature{}},
		FraudGuard:   api.clientReportFraudGuard(),
	}

	runtime := api.licensing()
	if runtime == nil || runtime.store == nil {
		return reply
	}
	reply.System.CanVerifyCertificates = runtime.store.Configured()

	serials := make([]string, 0, len(reply.Modules))
	for _, module := range reply.Modules {
		serials = append(serials, module.SN)
	}
	entitlements := runtime.store.Entitlements(machineID, serials, now)

	// Every feature in the catalogue, always, including the ones that are not
	// licensed. A report that listed only what was granted would make "you
	// never bought this" and "this feature no longer exists in the build" look
	// the same from the console.
	for _, feature := range []license.Feature{
		license.FeatureSMSForward,
		license.FeatureSMSSend,
		license.FeatureCallAnswer,
		license.FeatureCallDial,
		license.FeatureWireGuard,
	} {
		decision := entitlements.Check(feature)
		entry := clientReportFeature{
			Feature:  string(feature),
			Allowed:  decision.Allowed,
			Licensed: decision.Licensed,
			Reason:   decision.Reason,
		}
		for _, scope := range decision.Missing {
			entry.Missing = append(entry.Missing, string(scope))
		}
		reply.Entitlements.Features = append(reply.Entitlements.Features, entry)
	}

	for _, certificate := range entitlements.Certificates() {
		reply.Entitlements.Certificates = append(
			reply.Entitlements.Certificates, clientReportCertificate{
				ID:        certificate.ID,
				Scope:     string(certificate.Scope),
				MachineID: certificate.MachineID,
				ModuleSN:  certificate.ModuleSN,
				Features:  featureStrings(certificate.Features),
				IssuedAt:  certificate.IssuedAt,
				ExpiresAt: certificate.ExpiresAt,
			})
	}
	for _, rejection := range entitlements.Rejected() {
		reply.Entitlements.Rejected = append(
			reply.Entitlements.Rejected, clientReportRejection{
				ID:     rejection.Certificate.ID,
				Scope:  string(rejection.Certificate.Scope),
				Reason: rejection.Reason,
			})
	}
	return reply
}

// clientReportModules reports the hardware, and only the hardware.
//
// store.Device also carries CurrentICCID, SIMInserted and a whole SIM struct.
// Those identify the customer's SIM account rather than the module, so they are
// not copied here -- see the note at the top of this file, and the test that
// enforces it.
func (api *API) clientReportModules(ctx context.Context) []clientReportModule {
	modules := []clientReportModule{}
	if api.repository == nil {
		return modules
	}
	devices, err := api.repository.Devices(ctx)
	if err != nil {
		return modules
	}
	for _, device := range devices {
		serial := strings.TrimSpace(device.IMEI)
		if serial == "" {
			continue
		}
		modules = append(modules, clientReportModule{
			SN:       serial,
			Model:    device.Model,
			Firmware: device.Firmware,
			Present:  device.Present,
			LastSeen: device.LastSeen,
		})
	}
	sort.Slice(modules, func(a, b int) bool { return modules[a].SN < modules[b].SN })
	return modules
}

// clientReportFraudGuard reports the counters.
//
// COUNTS ONLY. callGuardState holds the actual destination numbers, and they
// stay where they are: how many distinct destinations an installation used
// today is the accountability record this guard exists to produce, and which
// numbers they were is a call log.
//
// Available is false when the guard has no data directory -- fixture mode, or a
// gateway whose storage is not ready. Reporting zeros for that would read as
// "no calls today", which is a different and wrong statement.
func (api *API) clientReportFraudGuard() clientReportFraudGuard {
	report := clientReportFraudGuard{}
	if guard, ok := api.callGuardStore(); ok && guard != nil {
		report.Call = guardSnapshot(guard)
	}
	if guard, ok := api.messageGuardStore(); ok && guard != nil {
		report.Message = guardSnapshot(guard)
	}
	report.Triggered = (report.Call.Available && !report.Call.Unlocked && report.Call.Remaining == 0) ||
		(report.Message.Available && !report.Message.Unlocked && report.Message.Remaining == 0)
	return report
}

func guardSnapshot(guard *callGuard) clientReportGuard {
	remaining, unlocked := guard.Remaining()
	snapshot := clientReportGuard{
		Limit:     guard.limit,
		Remaining: remaining,
		Unlocked:  unlocked,
		Available: true,
	}
	// Used is derived rather than read, so the destination list is never
	// touched here. An unlocked guard reports Remaining 0, which would make a
	// naive subtraction claim the limit was fully spent.
	if !unlocked {
		snapshot.Used = guard.limit - remaining
	}
	return snapshot
}

// issuerKeyFingerprint is the first four bytes of the issuer public key, hex.
//
// Empty when there is no usable key, which pairs with CanVerifyCertificates:
// a blank fingerprint and a false boolean say the same thing twice, on purpose,
// because this is the failure that looks exactly like an unpaid installation.
func issuerKeyFingerprint(publicKeyBase64 string) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil || len(decoded) != 32 {
		return ""
	}
	return hex.EncodeToString(decoded[:4])
}

// featureStrings copies and sorts, so the report does not depend on the order
// the certificate happened to be written in.
func featureStrings(features []string) []string {
	names := append([]string(nil), features...)
	sort.Strings(names)
	return names
}
