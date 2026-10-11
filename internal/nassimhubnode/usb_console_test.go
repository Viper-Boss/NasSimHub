package nassimhubnode

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func consoleTLS(t *testing.T) *httptest.Server {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
func TestUSBConsoleCertificateTrustDoesNotSkipTLSVerification(t *testing.T) {
	a, b := consoleTLS(t), consoleTLS(t)
	tr, e := usbConsoleTransport(a.TLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	defer tr.CloseIdleConnections()
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification disabled")
	}
	client := &http.Client{Transport: tr}
	r, e := client.Get(a.URL)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if _, e = client.Get(b.URL); e == nil {
		t.Fatal("another device certificate accepted")
	}
}
