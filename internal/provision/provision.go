// Package provision decides whether this installation's data directory
// belongs to this machine.
//
// The problem it exists for: a gateway is sold preloaded, so a deployment gets
// copied. Everything that makes an installation ITSELF lives in the data
// directory -- the administrator account, the NKN identity seed, the NKN node
// wallet, the message and contact database, the anti-fraud counters, the
// forwarding tokens, the TLS key. Copy that directory and every copy is the
// same installation:
//
//	the administrator account comes with it, so the first-run setup page
//	never appears and the customer's gateway trusts a password they were
//	never told;
//
//	the NKN identity comes with it, so every customer answers on the SAME
//	NKN address. Their messages collide, and anyone holding a copy of the
//	image can receive traffic meant for any of them;
//
//	the database comes with it, so whoever prepared the image ships their
//	own SMS, contacts and call history to every customer;
//
//	the anti-fraud counters come with it, so a new gateway can start the
//	day already at its limit.
//
// None of that announces itself. A cloned gateway boots, logs in, and works.
//
// So this package answers one question -- was this data directory created on
// this machine? -- and the gateway refuses to serve until a human resolves the
// answer. The evidence is already collected: internal/license records a
// hardware fingerprint the first time it resolves the machine ID, and that
// record was never read by anything until now.
package provision

import (
	"fmt"
	"sort"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// Status is what the rest of the system acts on.
type Status string

const (
	// StatusReady means the data directory belongs here, or there is not
	// enough evidence to say otherwise.
	StatusReady Status = "ready"
	// StatusCloned means the data directory was created on a different
	// machine. The gateway must not serve with the identity in it.
	StatusCloned Status = "cloned"
)

// Decision carries the answer and the evidence for it.
//
// The evidence is kept because a human has to act on this, and "your
// installation was copied" is an accusation that needs to be checkable. A
// support conversation that starts with the two machine IDs and which hardware
// sources disagree is a different conversation from one that starts with a
// wiped gateway.
type Decision struct {
	Status Status
	// Reason is a short operator-facing sentence, in the product's language.
	Reason string
	// StoredMachineID and CurrentMachineID are the two identities compared.
	StoredMachineID  string
	CurrentMachineID string
	// MatchingSources and DivergingSources name the hardware sources that
	// agreed and disagreed. A board with one part replaced shows some of each;
	// a different machine shows none matching.
	MatchingSources  []string
	DivergingSources []string
}

func (decision Decision) Cloned() bool { return decision.Status == StatusCloned }

// Detect compares the fingerprint recorded at activation with this machine's.
//
// DELIBERATELY CONSERVATIVE, and this is the whole design of the function. The
// consequence of a false positive is a customer told their working gateway is
// a copy, so "cloned" is returned only when the evidence is unambiguous:
//
//	there is a stored fingerprint (a fresh install has none);
//	this machine's fingerprint could be read at all (a container or a board
//	with no burned-in identifier yields nothing, and "I cannot tell" must
//	never become "you are a clone");
//	the derived machine IDs differ;
//	AND NOT ONE identifying hardware source has the same value.
//
// That last clause is what separates a copied directory from a repaired board.
// Replace the eMMC in a machine whose SoC serial is still readable and the
// machine ID changes while the SoC serial still matches -- some sources agree,
// so this returns ready and lets a human decide. Move the directory to a
// different machine and nothing agrees.
//
// The asymmetry is intentional: a clone that slips through is caught by the
// customer noticing somebody else's messages, which is bad. A false positive
// wipes a working installation, which is worse and unrecoverable.
func Detect(stored license.Fingerprint, storedFound bool, current license.Fingerprint, currentErr error) Decision {
	decision := Decision{
		Status:           StatusReady,
		StoredMachineID:  stored.MachineID,
		CurrentMachineID: current.MachineID,
	}
	if !storedFound || strings.TrimSpace(stored.MachineID) == "" {
		// A fresh installation. Nothing to compare against, and the record
		// will be written the first time the machine ID resolves.
		return decision
	}
	if currentErr != nil || strings.TrimSpace(current.MachineID) == "" {
		// This machine has no readable hardware identity. That is a legitimate
		// state -- see internal/license, where it also means no machine
		// licence can be bound -- and it is NOT evidence of a clone.
		decision.Reason = "本机无法读取硬件标识，无法判断数据目录归属"
		return decision
	}
	if stored.MachineID == current.MachineID {
		return decision
	}

	// The IDs differ. Now decide between "different machine" and "same machine,
	// changed part" by comparing the raw sources behind them.
	for name, storedValue := range stored.Sources {
		if strings.TrimSpace(storedValue) == "" {
			continue
		}
		currentValue := strings.TrimSpace(current.Sources[name])
		if currentValue == "" {
			// A source that has gone away is not agreement, but it is not
			// disagreement either -- it is absence. Counted as neither.
			continue
		}
		if currentValue == storedValue {
			decision.MatchingSources = append(decision.MatchingSources, name)
		} else {
			decision.DivergingSources = append(decision.DivergingSources, name)
		}
	}
	sort.Strings(decision.MatchingSources)
	sort.Strings(decision.DivergingSources)

	if len(decision.MatchingSources) > 0 {
		decision.Reason = fmt.Sprintf(
			"硬件标识部分变化（%s 仍一致，%s 已改变）。可能是更换了部件，请人工确认后再决定是否重新初始化。",
			strings.Join(decision.MatchingSources, "、"),
			strings.Join(decision.DivergingSources, "、"))
		return decision
	}

	// Nothing matched. This directory was created somewhere else.
	decision.Status = StatusCloned
	decision.Reason = "这份数据来自另一台设备：管理员账号、NKN 身份、短信与联系人都属于别人。" +
		"必须重新初始化后才能使用。"
	return decision
}
