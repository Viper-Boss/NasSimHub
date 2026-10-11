package nassimhubnode

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// USBAdministration never accepts a destination from the browser. The browser
// certificate is delivered over the already pinned and owner-authenticated
// Node protocol. Ordinary x509 chain, lifetime and hostname verification stay on.
func (f *Fleet) USBAdministration(ctx context.Context, id string, request *http.Request) (*http.Response, error) {
	live := f.lookup(id)
	if live == nil {
		return nil, ErrUnknownNode
	}
	live.mu.Lock()
	state, record := live.state, live.record
	live.mu.Unlock()
	if !state.Online || state.Revoked || !state.Compatible {
		return nil, ErrNodeUnavailable
	}
	endpoint, e := url.Parse(record.USBEndpoint)
	if e != nil || endpoint.Scheme != "https" || endpoint.Host != "10.55.0.2:7580" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("verified USB endpoint unavailable")
	}
	if request.URL.Path != "/" && !strings.HasPrefix(request.URL.Path, "/admin/") {
		return nil, errors.New("invalid console path")
	}
	if path.Clean(request.URL.Path) != request.URL.Path || request.URL.RawPath != "" {
		return nil, errors.New("invalid console path")
	}
	client, _ := live.handles()
	access, e := client.LocalAdministration(ctx)
	if e != nil {
		return nil, e
	}
	if access.Port != 7581 || len(access.CertificateDER) == 0 || len(access.CertificateDER) > 16384 {
		return nil, errors.New("device needs USB console support")
	}
	transport, e := usbConsoleTransport(access.CertificateDER)
	if e != nil {
		return nil, e
	}
	defer transport.CloseIdleConnections()
	proxy := &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: refuseRedirect}
	target := request.Clone(ctx)
	target.URL = &url.URL{Scheme: "https", Host: "10.55.0.2:7581", Path: request.URL.Path, RawQuery: request.URL.RawQuery}
	target.Host = target.URL.Host
	target.RequestURI = ""
	return proxy.Do(target)
}

func usbConsoleTransport(der []byte) (*http.Transport, error) {
	certificate, e := x509.ParseCertificate(der)
	if e != nil {
		return nil, errors.New("invalid console certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 40 * time.Second}, nil
}
