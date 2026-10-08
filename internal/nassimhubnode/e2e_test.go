package nassimhubnode_test

// End-to-end test against a real Node process.
//
// It runs only when NSH_E2E_NODE_BIN points at a mock-nassimhub-node binary
// built with -tls support (scripts/nassimhub-node-e2e.sh builds one). The Node
// then speaks the real protocol with the real pairing, identity and
// device-certificate TLS; only its modem is simulated. Nothing here touches
// hardware.

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/communication"
	"github.com/human-agent65535/modemdeck/internal/messageevents"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/platform/database"
	"github.com/human-agent65535/modemdeck/internal/store"
)

type e2eNode struct {
	t        *testing.T
	binary   string
	stateDir string
	usb      string
	wifi     string
	control  string
	scenario string
	// extraArgs are appended to the mock Node's command line, for tests that
	// need a particular security level or an older device.
	extraArgs []string
	cmd       *exec.Cmd
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func startNode(t *testing.T, binary, stateDir, usb, control string) *e2eNode {
	t.Helper()
	return startNodeWithWiFi(t, binary, stateDir, usb, "", control)
}

func startNodeWithWiFi(t *testing.T, binary, stateDir, usb, wifi, control string) *e2eNode {
	t.Helper()
	node := &e2eNode{t: t, binary: binary, stateDir: stateDir, usb: usb, wifi: wifi, control: control}
	node.start()
	t.Cleanup(node.stop)
	return node
}

func (n *e2eNode) start() {
	n.t.Helper()
	scenario := n.scenario
	if scenario == "" {
		scenario = "unicom"
	}
	arguments := append([]string{"-tls", "-platform", "msm8916", "-scenario", scenario,
		"-state-dir", n.stateDir, "-usb-listen", n.usb, "-wifi-listen", n.wifi, "-control-listen", n.control},
		n.extraArgs...)
	n.cmd = exec.Command(n.binary, arguments...)
	n.cmd.Stdout = io.Discard
	n.cmd.Stderr = io.Discard
	if err := n.cmd.Start(); err != nil {
		n.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if connection, err := net.DialTimeout("tcp", n.usb, 200*time.Millisecond); err == nil {
			_ = connection.Close()
			// The control listener starts after USB. Starting a test as soon as
			// USB opens races scenario injection against the control startup.
			if n.control != "" {
				control, err := net.DialTimeout("tcp", n.control, 200*time.Millisecond)
				if err != nil {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				_ = control.Close()
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	n.t.Fatal("node did not start")
}

func (n *e2eNode) stop() {
	if n.cmd != nil && n.cmd.Process != nil {
		_ = n.cmd.Process.Kill()
		_, _ = n.cmd.Process.Wait()
		n.cmd = nil
	}
}

func (n *e2eNode) setLink(kind string, up bool) {
	n.t.Helper()
	response, err := http.Post(fmt.Sprintf("http://%s/control/link?type=%s&up=%t", n.control, kind, up), "text/plain", nil)
	if err != nil {
		n.t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode/100 != 2 {
		n.t.Fatalf("set link %s: %d", kind, response.StatusCode)
	}
}

func (n *e2eNode) injectSMS(from, text string) {
	n.t.Helper()
	target := "http://" + n.control + "/control/sms/incoming?from=" + url.QueryEscape(from) + "&text=" + url.QueryEscape(text)
	response, err := http.Post(target, "text/plain", nil)
	if err != nil {
		n.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(response.Body)
		n.t.Fatalf("inject sms: %d %s", response.StatusCode, body)
	}
}

// publicDocument reads GET /v1/node without trusting anything, the way a human
// with curl would, to observe the Node's own view of its pairing.
func (n *e2eNode) publicDocument() map[string]any {
	n.t.Helper()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // observation only
	}}
	response, err := client.Get("https://" + n.usb + "/v1/node")
	if err != nil {
		n.t.Fatal(err)
	}
	defer response.Body.Close()
	var document map[string]any
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		n.t.Fatal(err)
	}
	return document
}

// hostWithoutModem is a host agent on a NAS that has no modem of its own.
type hostWithoutModem struct{}

func (hostWithoutModem) Health(context.Context) (agentclient.Health, error) {
	return agentclient.Health{Status: "ok", APIVersion: agentclient.APIVersion, AgentVersion: "test",
		Provider: agentclient.ProviderHealth{Name: "modemmanager", Available: false}}, nil
}
func (hostWithoutModem) Snapshot(context.Context) (agentclient.Snapshot, error) {
	return agentclient.Snapshot{}, errors.New("no modem")
}
func (hostWithoutModem) StartCall(context.Context, agentclient.StartCallRequest) (agentclient.CommandReceipt, error) {
	return agentclient.CommandReceipt{}, errors.New("no modem")
}
func (hostWithoutModem) CallAction(context.Context, string, string, agentclient.CallActionRequest) (agentclient.CommandReceipt, error) {
	return agentclient.CommandReceipt{}, errors.New("no modem")
}
func (hostWithoutModem) SendDTMF(context.Context, string, agentclient.DTMFRequest) (agentclient.CommandReceipt, error) {
	return agentclient.CommandReceipt{}, errors.New("no modem")
}
func (hostWithoutModem) SendMessage(context.Context, agentclient.SendMessageRequest) (agentclient.CommandReceipt, error) {
	return agentclient.CommandReceipt{}, errors.New("host agent must not receive a node message")
}
func (hostWithoutModem) DeviceConfiguration(context.Context, string) (agentclient.DeviceConfiguration, error) {
	return agentclient.DeviceConfiguration{}, errors.New("no modem")
}
func (hostWithoutModem) ApplyDeviceConfiguration(context.Context, string, agentclient.ApplyDeviceConfigurationRequest) (agentclient.DeviceConfiguration, error) {
	return agentclient.DeviceConfiguration{}, errors.New("no modem")
}
func (hostWithoutModem) SystemMetrics(context.Context) (agentclient.SystemMetrics, error) {
	return agentclient.SystemMetrics{}, errors.New("no modem")
}
func (hostWithoutModem) VowifiDiagnostics(context.Context) (agentclient.VowifiDiagnostics, error) {
	return agentclient.VowifiDiagnostics{}, errors.New("no modem")
}

type gateway struct {
	db         *sql.DB
	repository *store.Store
	fleet      *nassimhubnode.Fleet
	service    *communication.Service
}

func openGateway(t *testing.T, ctx context.Context, dataDir, usbURL string) gateway {
	t.Helper()
	db, err := database.Open(ctx, database.Config{TargetPath: filepath.Join(dataDir, "modemdeck.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := nassimhubnode.LoadOrCreateIdentity(filepath.Join(dataDir, "nassimhub"))
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := nassimhubnode.New(ctx, nassimhubnode.Options{
		Identity: identity, Repository: repository, CoreVersion: "1.15.0",
		USBBaseURL: usbURL, Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := communication.New(hostWithoutModem{}, repository, messageevents.NewBuffer(64))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetNodeSource(fleet); err != nil {
		t.Fatal(err)
	}
	return gateway{db: db, repository: repository, fleet: fleet, service: service}
}

func (g gateway) sync(t *testing.T, ctx context.Context) communication.Status {
	t.Helper()
	g.fleet.PollAll(ctx)
	status, err := g.service.Refresh(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return status
}

func nodeLine(t *testing.T, status communication.Status) store.LineSummary {
	t.Helper()
	for _, line := range status.Lines {
		if strings.HasPrefix(line.EndpointID, store.NasSimHubNodeEndpointPrefix) {
			return line
		}
	}
	t.Fatalf("no node line in status: %+v", status.Lines)
	return store.LineSummary{}
}

func messages(t *testing.T, ctx context.Context, repository *store.Store, lineID string) []store.Message {
	t.Helper()
	list, err := repository.Messages(ctx, store.MessageQuery{LineIDs: []string{lineID}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestE2ENodePairingSMSAndTrust(t *testing.T) {
	binary := os.Getenv("NSH_E2E_NODE_BIN")
	if binary == "" {
		t.Skip("set NSH_E2E_NODE_BIN to a mock-nassimhub-node binary (see scripts/nassimhub-node-e2e.sh)")
	}
	ctx := context.Background()
	work := t.TempDir()
	usb, control := freePort(t), freePort(t)
	usbURL := "https://" + usb
	_, port, _ := net.SplitHostPort(usb)
	wifi := net.JoinHostPort("127.0.0.2", port)
	node := startNodeWithWiFi(t, binary, filepath.Join(work, "node-a"), usb, wifi, control)
	dataDir := filepath.Join(work, "nas")

	g := openGateway(t, ctx, dataDir, usbURL)

	// Discovery: verified identity, one ticket.
	candidates, err := g.fleet.Discover(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("discover: %+v %v", candidates, err)
	}
	candidate := candidates[0]
	if !strings.HasPrefix(candidate.DeviceID, "NSH-410-") || candidate.Ticket == "" || candidate.Fingerprint == "" ||
		candidate.PairingState != "UNPAIRED" || !candidate.Compatible {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}

	// A wrong fingerprint is refused and burns the ticket.
	if _, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{Ticket: candidate.Ticket, DeviceID: candidate.DeviceID, Fingerprint: "0000 0000"}); !errors.Is(err, nassimhubnode.ErrFingerprintMismatch) {
		t.Fatalf("wrong fingerprint: %v", err)
	}
	if _, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{Ticket: candidate.Ticket, DeviceID: candidate.DeviceID, Fingerprint: candidate.Fingerprint}); !errors.Is(err, nassimhubnode.ErrTicketInvalid) {
		t.Fatalf("reused ticket must be refused: %v", err)
	}
	if document := node.publicDocument(); document["pairing_state"] != "UNPAIRED" {
		t.Fatalf("refused pairing changed the node: %v", document["pairing_state"])
	}

	candidates, _ = g.fleet.Discover(ctx)
	candidate = candidates[0]
	view, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{Ticket: candidate.Ticket, DeviceID: candidate.DeviceID,
		Fingerprint: strings.ToLower(strings.ReplaceAll(candidate.Fingerprint, " ", "")), DisplayName: "客厅棒子"})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if view.PairState != "paired" || !view.Online || view.DeviceID != candidate.DeviceID {
		t.Fatalf("unexpected paired view: %+v", view)
	}
	if document := node.publicDocument(); document["pairing_state"] != "PAIRED" || document["paired_core_id"] != g.fleet.CoreID() {
		t.Fatalf("node does not show this gateway as owner: %v", document)
	}

	// A second discovery offers no ticket for an already-paired device.
	candidates, _ = g.fleet.Discover(ctx)
	if len(candidates) != 1 || candidates[0].Ticket != "" || !candidates[0].Known {
		t.Fatalf("paired device must not be re-offered: %+v", candidates)
	}

	// Incoming SMS reaches the ordinary inbox, tagged with node and SIM.
	node.injectSMS("10010", "您的验证码是 482913，5 分钟内有效。")
	long := strings.Repeat("长短信合并测试。", 30)
	node.injectSMS("+8613800138000", long)
	status := g.sync(t, ctx)
	line := nodeLine(t, status)
	if !line.Capabilities.SendMessage {
		t.Fatalf("node line cannot send: %+v", line.Capabilities)
	}
	inbox := messages(t, ctx, g.repository, line.ID)
	incoming := 0
	for _, message := range inbox {
		if message.Direction == "incoming" {
			incoming++
		}
	}
	if incoming != 2 {
		t.Fatalf("want 2 incoming messages, got %d: %+v", incoming, inbox)
	}
	var tagged int
	if err := g.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sms WHERE node_id = ? AND sim_id <> ''`, candidate.DeviceID).Scan(&tagged); err != nil || tagged != 2 {
		t.Fatalf("messages not tagged with node/sim: %d %v", tagged, err)
	}

	// Polling again is idempotent.
	g.sync(t, ctx)
	if again := messages(t, ctx, g.repository, line.ID); len(again) != len(inbox) {
		t.Fatalf("re-poll duplicated messages: %d -> %d", len(inbox), len(again))
	}

	// Sending goes to the node exactly once, even when the request is replayed.
	sent, err := g.service.SendMessage(ctx, communication.SendMessageInput{LineID: line.ID, Number: "10010", Text: "查询余额", RequestID: "e2e-send-1"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if sent.Direction != "outgoing" {
		t.Fatalf("unexpected sent message: %+v", sent)
	}
	replayed, err := g.service.SendMessage(ctx, communication.SendMessageInput{LineID: line.ID, Number: "10010", Text: "查询余额", RequestID: "e2e-send-1"})
	if err != nil || replayed.ID != sent.ID {
		t.Fatalf("replay must return the same message: %+v %v", replayed, err)
	}
	g.sync(t, ctx)
	outgoing := 0
	for _, message := range messages(t, ctx, g.repository, line.ID) {
		if message.Direction == "outgoing" {
			outgoing++
		}
	}
	if outgoing != 1 {
		t.Fatalf("want exactly one outgoing row, got %d", outgoing)
	}

	// Gateway restart: trust persists, no pairing needed.
	g2 := openGateway(t, ctx, dataDir, usbURL)
	if g2.fleet.CoreID() != g.fleet.CoreID() {
		t.Fatal("core identity changed across restart")
	}
	status = g2.sync(t, ctx)
	nodes := g2.fleet.Nodes()
	if len(nodes) != 1 || !nodes[0].Online || nodes[0].Revoked {
		t.Fatalf("paired node not restored after restart: %+v", nodes)
	}
	if nodeLine(t, status).ID != line.ID {
		t.Fatal("node line identity changed across restart")
	}

	// Node restart: same key, same device, still paired.
	node.stop()
	g2.fleet.PollAll(ctx)
	if nodes := g2.fleet.Nodes(); nodes[0].Online {
		t.Fatal("stopped node still reported online")
	}
	node.start()
	g2.fleet.PollAll(ctx)
	if nodes := g2.fleet.Nodes(); !nodes[0].Online {
		t.Fatalf("restarted node not back online: %+v", nodes[0])
	}
	g2.sync(t, ctx)
	if after := messages(t, ctx, g2.repository, line.ID); len(after) != len(inbox)+1 {
		t.Fatalf("node restart changed the inbox: %d rows, want %d", len(after), len(inbox)+1)
	}

	// USB pulled: the same device continues over Wi-Fi, no second device.
	if err := g2.fleet.SetWiFiEndpoint(ctx, candidate.DeviceID, "https://"+wifi); err != nil {
		t.Fatal(err)
	}
	node.setLink("usb", false)
	status = g2.sync(t, ctx)
	nodes = g2.fleet.Nodes()
	if len(nodes) != 1 || !nodes[0].Online || nodes[0].Transport != "wifi" {
		t.Fatalf("wifi failover failed: %+v", nodes)
	}
	if nodeLine(t, status).ID != line.ID {
		t.Fatal("wifi failover produced a different line")
	}
	node.injectSMS("10010", "经 Wi-Fi 收到")
	g2.sync(t, ctx)
	if after := messages(t, ctx, g2.repository, line.ID); len(after) != len(inbox)+2 {
		t.Fatalf("wifi-path message count %d, want %d", len(after), len(inbox)+2)
	}
	node.setLink("usb", true)

	// Same key, new certificate (the device re-issued it), while Wi-Fi is
	// down: the device stays trusted - it chains to the pinned certificate
	// because the key is the same, and the re-pin path covers an expired one -
	// and is not reported as an impostor.
	before, err := g2.repository.NasSimHubNodeRecord(ctx, candidate.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	node.stop()
	removeCertificates(t, node.stateDir)
	node.start()
	node.setLink("wifi", false)
	g2.fleet.PollAll(ctx)
	if nodes := g2.fleet.Nodes(); !nodes[0].Online || nodes[0].Transport != "usb" {
		t.Fatalf("renewed certificate not re-pinned: %+v", nodes[0])
	}
	after, err := g2.repository.NasSimHubNodeRecord(ctx, candidate.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.PublicKey, before.PublicKey) {
		t.Fatal("certificate renewal changed the pinned key")
	}
	node.setLink("wifi", true)

	// A different device at the same address is refused by the pin, and the
	// diagnosis says so even though the Wi-Fi endpoint is unreachable too.
	node.stop()
	impostor := startNode(t, binary, filepath.Join(work, "impostor"), usb, freePort(t))
	g2.fleet.PollAll(ctx)
	nodes = g2.fleet.Nodes()
	if nodes[0].Online || !strings.Contains(nodes[0].LastError, "pinned") {
		t.Fatalf("impostor must fail the pin: %+v", nodes[0])
	}
	impostor.stop()
	node.start()

	// Unpair releases the device and forgets it.
	released, err := g2.fleet.Unpair(ctx, candidate.DeviceID)
	if err != nil || !released {
		t.Fatalf("unpair: released=%v err=%v", released, err)
	}
	if document := node.publicDocument(); document["pairing_state"] != "UNPAIRED" {
		t.Fatalf("node still paired after unpair: %v", document["pairing_state"])
	}
	if _, err := g2.repository.NasSimHubNodeRecord(ctx, candidate.DeviceID); !errors.Is(err, store.ErrNasSimHubNodeNotFound) {
		t.Fatalf("trust record survived unpair: %v", err)
	}
	fmt.Fprintln(io.Discard, status)
}

func removeCertificates(t *testing.T, dir string) {
	t.Helper()
	removed := 0
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "device-cert.pem" {
			removed++
			return os.Remove(path)
		}
		return nil
	})
	if err != nil || removed == 0 {
		t.Fatalf("remove device certificate: removed=%d err=%v", removed, err)
	}
}

// Call control over the real Node protocol: pinned TLS, paired session,
// request-ID mapping and receipt checks. The mock "telecom" scenario offers
// voice control, as a Node with VoiceWrite enabled does.
func TestE2ENodeCallControl(t *testing.T) {
	binary := os.Getenv("NSH_E2E_NODE_BIN")
	if binary == "" {
		t.Skip("set NSH_E2E_NODE_BIN to a mock-nassimhub-node binary (see scripts/nassimhub-node-e2e.sh)")
	}
	ctx := context.Background()
	work := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := &e2eNode{t: t, binary: binary, stateDir: filepath.Join(work, "node"), usb: usb, control: control, scenario: "telecom"}
	node.start()
	t.Cleanup(node.stop)
	g := openGateway(t, ctx, filepath.Join(work, "nas"), "https://"+usb)
	candidates, err := g.fleet.Discover(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("discover: %+v %v", candidates, err)
	}
	if _, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{Ticket: candidates[0].Ticket,
		DeviceID: candidates[0].DeviceID, Fingerprint: candidates[0].Fingerprint}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	g.fleet.PollAll(ctx)
	endpoint := "nsh:" + candidates[0].DeviceID + ":0"
	calls := func() []agentclient.Call {
		_, _, current, _, _ := g.fleet.NodeSnapshot()
		return current
	}

	// Outgoing: dial, observe, hang up.
	receipt, err := g.fleet.StartCall(ctx, agentclient.StartCallRequest{RequestID: "e2e-dial-1", LineID: endpoint, Number: "10000"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if receipt.RequestID != "e2e-dial-1" || receipt.ResourceID == "" {
		t.Fatalf("dial receipt = %+v", receipt)
	}
	if err := g.fleet.RefreshEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	current := calls()
	if len(current) != 1 || current[0].ID != receipt.ResourceID || current[0].Direction != "outgoing" ||
		current[0].LineID != endpoint || current[0].Number != "10000" {
		t.Fatalf("dialled call not observed: %+v", current)
	}
	// Retrying the same request reaches the Node's own idempotency record.
	again, err := g.fleet.StartCall(ctx, agentclient.StartCallRequest{RequestID: "e2e-dial-1", LineID: endpoint, Number: "10000"})
	if err == nil && again.ResourceID != receipt.ResourceID {
		t.Fatalf("retried dial placed a second call: %+v vs %+v", again, receipt)
	}
	if _, err := g.fleet.CallAction(ctx, endpoint, receipt.ResourceID, "hangup", agentclient.CallActionRequest{RequestID: "e2e-hangup-1"}); err != nil {
		t.Fatalf("hangup: %v", err)
	}
	if err := g.fleet.RefreshEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if current := calls(); len(current) != 0 {
		t.Fatalf("hung-up call still projected: %+v", current)
	}

	// Incoming: ring, answer, hang up.
	response, err := http.Post(fmt.Sprintf("http://%s/control/call/incoming?from=%%2B8613900139000", control), "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := g.fleet.RefreshEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	current = calls()
	if len(current) != 1 || current[0].Direction != "incoming" || current[0].State != "ringing" {
		t.Fatalf("incoming call not ringing: %+v", current)
	}
	incoming := current[0].ID
	if _, err := g.fleet.CallAction(ctx, endpoint, incoming, "answer", agentclient.CallActionRequest{RequestID: "e2e-answer"}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	g.fleet.RefreshEndpoint(ctx, endpoint) //nolint:errcheck
	if current := calls(); len(current) != 1 || current[0].State != "active" {
		t.Fatalf("answered call not active: %+v", current)
	}
	if _, err := g.fleet.CallAction(ctx, endpoint, incoming, "hangup", agentclient.CallActionRequest{RequestID: "e2e-hangup-2"}); err != nil {
		t.Fatalf("hangup incoming: %v", err)
	}
	g.fleet.RefreshEndpoint(ctx, endpoint) //nolint:errcheck
	if current := calls(); len(current) != 0 {
		t.Fatalf("incoming call still projected after hangup: %+v", current)
	}

	// A call ID the Node does not know is a definite refusal.
	_, err = g.fleet.CallAction(ctx, endpoint, "call-999", "hangup", agentclient.CallActionRequest{RequestID: "e2e-hangup-3"})
	var definite *agentclient.OperationError
	if !errors.As(err, &definite) {
		t.Fatalf("unknown call hangup = %v, want a definite refusal", err)
	}
}
