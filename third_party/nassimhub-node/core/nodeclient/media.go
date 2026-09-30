package nodeclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenMedia upgrades one authenticated, pinned TLS connection to the Node's
// fixed MDM2 PCM stream. The returned stream stays open beyond the short
// request context used to establish it; the caller must Close it.
func (c *Client) OpenMedia(ctx context.Context, callID string) (io.ReadWriteCloser, error) {
	if !validMediaCallID(callID) {
		return nil, errors.New("invalid Node media call ID")
	}
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	endpoint, err := c.selector.Active(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	token := c.session.Token
	c.mu.Unlock()
	lifetime, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(lifetime, http.MethodGet,
		endpoint.BaseURL+"/v1/calls/"+neturl.PathEscape(callID)+"/media", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "modemdeck-media-v2")
	handshakeTimeout := c.http.Timeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 10 * time.Second
	}
	timer := time.AfterFunc(handshakeTimeout, cancel)
	client := &http.Client{
		Transport: c.http.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("media upgrade must not redirect")
		},
	}
	response, err := client.Do(request)
	timer.Stop()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open Node media: %w", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		defer response.Body.Close()
		cancel()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("Node media returned HTTP %d", response.StatusCode)
	}
	if !validMediaHeaders(response.Header) {
		_ = response.Body.Close()
		cancel()
		return nil, errors.New("Node media format or protocol is unsupported")
	}
	stream, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		_ = response.Body.Close()
		cancel()
		return nil, errors.New("Node media upgrade has no duplex body")
	}
	return &mediaStream{ReadWriteCloser: stream, cancel: cancel}, nil
}

func validMediaCallID(id string) bool {
	if !strings.HasPrefix(id, "mm-") {
		return false
	}
	_, err := strconv.ParseUint(strings.TrimPrefix(id, "mm-"), 10, 32)
	return err == nil
}

func validMediaHeaders(headers http.Header) bool {
	expected := map[string]string{
		"Upgrade":                           "modemdeck-media-v2",
		"ModemDeck-Media-Version":           "2",
		"ModemDeck-Media-Encoding":          "pcm",
		"ModemDeck-Media-Resolution":        "s16le",
		"ModemDeck-Media-Rate":              "16000",
		"ModemDeck-Media-Channels":          "1",
		"ModemDeck-Media-Frame-Duration-Ms": "20",
		"ModemDeck-Media-Frame-Bytes":       "640",
	}
	for name, value := range expected {
		if !strings.EqualFold(strings.TrimSpace(headers.Get(name)), value) {
			return false
		}
	}
	return true
}

type mediaStream struct {
	io.ReadWriteCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (s *mediaStream) Close() error {
	var err error
	s.once.Do(func() {
		err = s.ReadWriteCloser.Close()
		s.cancel()
	})
	return err
}
