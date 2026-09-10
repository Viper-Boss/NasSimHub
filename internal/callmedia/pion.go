package callmedia

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

const (
	maxOfferSDPBytes = 64 << 10
	opusFMTP         = "minptime=10;useinbandfec=1;stereo=0;sprop-stereo=0"
)

func opusRTPParameters() webrtc.RTPCodecParameters {
	return webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   RTPClockRate,
			Channels:    2,
			SDPFmtpLine: opusFMTP,
		},
		PayloadType: OpusPayloadType,
	}
}

// normalizePublicIPs validates the operator-supplied external address list.
// Entries are trimmed and blanks are dropped; an unparseable entry is rejected
// rather than silently ignored, because a typo would otherwise produce a
// candidate the browser can never reach.
func normalizePublicIPs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	ips := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if net.ParseIP(trimmed) == nil {
			return nil, fmt.Errorf(
				"call media public IP %q is not a valid IP address",
				trimmed,
			)
		}
		ips = append(ips, trimmed)
	}
	if len(ips) == 0 {
		return nil, nil
	}
	return ips, nil
}

// newWebRTCAPI builds the Pion API used for every browser call.
//
// publicIPs and udpPort exist because the API runs inside a container whose
// own addresses are not reachable from the browser. Without them Pion would
// advertise container-internal host candidates and ICE could never complete.
// Both are optional: an empty publicIPs and a zero udpPort preserve the
// previous behaviour exactly.
//
// The returned closer releases the shared UDP mux, if one was created. It is
// always non-nil so callers can defer it unconditionally.
func newWebRTCAPI(publicIPs []string, udpPort int) (*webrtc.API, func() error, error) {
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(opusRTPParameters(), webrtc.RTPCodecTypeAudio); err != nil {
		return nil, nil, err
	}
	interceptors := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(engine, interceptors); err != nil {
		return nil, nil, err
	}

	validatedIPs, err := normalizePublicIPs(publicIPs)
	if err != nil {
		return nil, nil, err
	}
	if validatedIPs == nil && udpPort == 0 {
		return webrtc.NewAPI(
			webrtc.WithMediaEngine(engine),
			webrtc.WithInterceptorRegistry(interceptors),
		), func() error { return nil }, nil
	}
	settings := webrtc.SettingEngine{}
	if validatedIPs != nil {
		// Rewrite the container-local host candidates to the address the
		// browser actually reaches the host by.
		settings.SetNAT1To1IPs(validatedIPs, webrtc.ICECandidateTypeHost)
	}
	closer := func() error { return nil }
	if udpPort != 0 {
		// One shared UDP port carries every call, so a single container port
		// mapping is enough and the browser can reach it predictably.
		mux, err := ice.NewMultiUDPMuxFromPort(udpPort)
		if err != nil {
			return nil, nil, fmt.Errorf("create ICE UDP mux on port %d: %w", udpPort, err)
		}
		settings.SetICEUDPMux(mux)
		closer = mux.Close
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(engine),
		webrtc.WithInterceptorRegistry(interceptors),
		webrtc.WithSettingEngine(settings),
	)
	return api, closer, nil
}

func validateOfferSDP(value string) error {
	if value == "" || len(value) > maxOfferSDPBytes {
		return ErrInvalidOffer
	}
	description := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: value}
	parsed, err := description.Unmarshal()
	if err != nil || len(parsed.MediaDescriptions) == 0 {
		return ErrInvalidOffer
	}
	for _, media := range parsed.MediaDescriptions {
		if !strings.EqualFold(media.MediaName.Media, "audio") {
			return ErrUnsupportedMedia
		}
	}
	if len(parsed.MediaDescriptions) != 1 {
		return ErrInvalidOffer
	}
	media := parsed.MediaDescriptions[0]
	if media.MediaName.Port.Value == 0 {
		return ErrUnsupportedDirection
	}
	direction, validDirection := mediaDirection(parsed.Attributes, media.Attributes)
	if !validDirection {
		return ErrInvalidOffer
	}
	if direction != "sendrecv" {
		return ErrUnsupportedDirection
	}
	if !offersOpus(media) {
		return ErrUnsupportedCodec
	}
	if !validPacketTime(parsed.Attributes, media.Attributes) {
		return ErrInvalidOffer
	}
	return nil
}

func offersOpus(media *sdp.MediaDescription) bool {
	formats := make(map[string]struct{}, len(media.MediaName.Formats))
	for _, format := range media.MediaName.Formats {
		formats[format] = struct{}{}
	}
	for _, attribute := range media.Attributes {
		if !strings.EqualFold(attribute.Key, "rtpmap") {
			continue
		}
		fields := strings.Fields(attribute.Value)
		if len(fields) != 2 {
			continue
		}
		if _, offered := formats[fields[0]]; !offered {
			continue
		}
		encoding := strings.Split(fields[1], "/")
		if len(encoding) != 3 || !strings.EqualFold(encoding[0], "opus") {
			continue
		}
		rate, rateErr := strconv.Atoi(encoding[1])
		channels, channelErr := strconv.Atoi(encoding[2])
		if rateErr == nil && channelErr == nil && rate == RTPClockRate && channels == 2 {
			return true
		}
	}
	return false
}

func mediaDirection(session, media []sdp.Attribute) (string, bool) {
	direction, found, valid := directionIn(session)
	if !valid {
		return "", false
	}
	if !found {
		direction = "sendrecv"
	}
	mediaDirection, mediaFound, mediaValid := directionIn(media)
	if !mediaValid {
		return "", false
	}
	if mediaFound {
		direction = mediaDirection
	}
	return direction, true
}

func directionIn(attributes []sdp.Attribute) (string, bool, bool) {
	direction := ""
	for _, attribute := range attributes {
		if !isDirection(attribute.Key) {
			continue
		}
		if direction != "" {
			return "", true, false
		}
		direction = strings.ToLower(attribute.Key)
	}
	return direction, direction != "", true
}

func isDirection(value string) bool {
	switch strings.ToLower(value) {
	case "sendrecv", "sendonly", "recvonly", "inactive":
		return true
	default:
		return false
	}
}

func validPacketTime(session, media []sdp.Attribute) bool {
	ptime, ptimeSet, ptimeOK := packetTime(session, media, "ptime")
	maxptime, maxptimeSet, maxptimeOK := packetTime(session, media, "maxptime")
	if !ptimeOK || !maxptimeOK {
		return false
	}
	if ptimeSet && !validPacketTimeValue(ptime) {
		return false
	}
	if maxptimeSet && !validPacketTimeValue(maxptime) {
		return false
	}
	return !ptimeSet || !maxptimeSet || ptime <= maxptime
}

func packetTime(session, media []sdp.Attribute, key string) (float64, bool, bool) {
	value, found, valid := packetTimeIn(session, key)
	if !valid {
		return 0, false, false
	}
	mediaValue, mediaFound, mediaValid := packetTimeIn(media, key)
	if !mediaValid {
		return 0, false, false
	}
	if mediaFound {
		return mediaValue, true, true
	}
	return value, found, true
}

func packetTimeIn(attributes []sdp.Attribute, key string) (float64, bool, bool) {
	var value float64
	found := false
	for _, attribute := range attributes {
		if !strings.EqualFold(attribute.Key, key) {
			continue
		}
		if found {
			return 0, true, false
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(attribute.Value), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, true, false
		}
		value = parsed
		found = true
	}
	return value, found, true
}

func validPacketTimeValue(milliseconds float64) bool {
	duration := time.Duration(math.Round(milliseconds*1000)) * time.Microsecond
	if math.Abs(float64(duration)/float64(time.Millisecond)-milliseconds) > 1e-9 {
		return false
	}
	return validOpusPacketDuration(duration)
}

func setAnswerPacketTime(answer webrtc.SessionDescription, duration time.Duration) (webrtc.SessionDescription, error) {
	parsed, err := answer.Unmarshal()
	if err != nil || !validOpusFrameDuration(duration) {
		return webrtc.SessionDescription{}, ErrNegotiation
	}
	foundAudio := false
	for _, media := range parsed.MediaDescriptions {
		if !strings.EqualFold(media.MediaName.Media, "audio") {
			return webrtc.SessionDescription{}, ErrUnsupportedMedia
		}
		foundAudio = true
		filtered := media.Attributes[:0]
		for _, attribute := range media.Attributes {
			if strings.EqualFold(attribute.Key, "ptime") || strings.EqualFold(attribute.Key, "maxptime") {
				continue
			}
			filtered = append(filtered, attribute)
		}
		media.Attributes = append(
			filtered,
			sdp.Attribute{
				Key:   "ptime",
				Value: strconv.FormatFloat(float64(duration)/float64(time.Millisecond), 'f', -1, 64),
			},
			sdp.Attribute{Key: "maxptime", Value: "120"},
		)
	}
	if !foundAudio {
		return webrtc.SessionDescription{}, ErrNegotiation
	}
	encoded, err := parsed.Marshal()
	if err != nil {
		return webrtc.SessionDescription{}, ErrNegotiation
	}
	answer.SDP = string(encoded)
	return answer, nil
}

func validateAnswerSDP(value string) error {
	description := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: value}
	parsed, err := description.Unmarshal()
	if err != nil || len(parsed.MediaDescriptions) != 1 {
		return ErrNegotiation
	}
	media := parsed.MediaDescriptions[0]
	if !strings.EqualFold(media.MediaName.Media, "audio") {
		return ErrUnsupportedMedia
	}
	if !offersOpus(media) {
		return fmt.Errorf("validate WebRTC answer: %w", ErrUnsupportedCodec)
	}
	return nil
}
