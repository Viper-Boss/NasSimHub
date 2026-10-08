package nassimhubnode

import (
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/nassimhub-node/proto"
	"testing"
)

func TestNodeVoiceProjectionRequiresCompleteReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*nodeState)
		want   bool
	}{
		{"ready", func(*nodeState) {}, true},
		{"no audio", func(s *nodeState) { s.Capabilities.VoiceAudio = false }, false},
		{"no control", func(s *nodeState) { s.Capabilities.VoiceControl = false }, false},
		{"IMS unknown", func(s *nodeState) { s.Capabilities.VoLTE = proto.TriUnknown }, false},
		{"offline", func(s *nodeState) { s.Online = false }, false},
		{"SIM missing", func(s *nodeState) { s.Status.SIM.State = proto.SIMMissing }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := readyState()
			s.Capabilities.VoLTE = proto.TriYes
			s.Capabilities.VoiceControl = true
			s.Capabilities.VoiceAudio = true
			tc.change(&s)
			c := projectNodeLine("node", "nsh:node:0", s).Capabilities
			if c.Dial != tc.want || c.AnswerCall != tc.want || c.HangupCall != tc.want || c.RejectCall != tc.want || c.Media != tc.want || c.SendDTMF {
				t.Fatalf("unexpected capabilities: %+v", c)
			}
		})
	}
}

func TestNodeDTMFProjectionRequiresExplicitCapabilityAndVoiceReadiness(t *testing.T) {
	s := readyState()
	s.Capabilities.VoLTE = proto.TriYes
	s.Capabilities.VoiceControl, s.Capabilities.VoiceAudio = true, true
	if projectNodeLine("node", "nsh:node:0", s).Capabilities.SendDTMF {
		t.Fatal("missing DTMF advertised")
	}
	s.Capabilities.DTMF = true
	if !projectNodeLine("node", "nsh:node:0", s).Capabilities.SendDTMF {
		t.Fatal("verified DTMF not projected")
	}
	s.Online = false
	if projectNodeLine("node", "nsh:node:0", s).Capabilities.SendDTMF {
		t.Fatal("offline DTMF advertised")
	}
}
func TestNodeActiveCallCarriesWidebandMedia(t *testing.T) {
	s := readyState()
	s.CallsKnown = true
	s.Capabilities.VoLTE = proto.TriYes
	s.Capabilities.VoiceControl = true
	s.Capabilities.VoiceAudio = true
	s.Calls = []proto.Call{{ID: "mm-1", SIMID: s.Status.SIM.ICCID, State: proto.CallActive}, {ID: "mm-2", SIMID: s.Status.SIM.ICCID, State: proto.CallRinging}}
	l := &liveNode{record: store.NasSimHubNodeRecord{ID: "node"}, state: s}
	calls := l.callContribution()
	if len(calls) != 2 {
		t.Fatalf("calls=%d", len(calls))
	}
	if !calls[0].MediaAvailable || !calls[0].MediaConfigured || calls[0].AudioFormat.Rate != 16000 || calls[0].AudioPort != "nsh:q6voice" || calls[1].MediaAvailable {
		t.Fatalf("unexpected media: %+v", calls)
	}
}
