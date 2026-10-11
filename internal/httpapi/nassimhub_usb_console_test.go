package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type usbTestFleet struct {
	fakeNodeFleet
	called int
	last   *http.Request
}

func (f *usbTestFleet) USBAdministration(_ context.Context, _ string, r *http.Request) (*http.Response, error) {
	f.called++
	f.last = r
	h := http.Header{}
	h.Set("Content-Type", "text/html")
	h.Add("Set-Cookie", "__Host-nsh-admin=device-session; Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age=86400")
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(`<head><link href="/admin/style.css"><script src="/admin/app.js"></script></head><a class="brand" href="/">AirSimHub</a>`))}, nil
}
func TestUSBConsoleSeparatesCookiesAndRewritesAssets(t *testing.T) {
	f := &usbTestFleet{}
	a := &API{nodeFleet: f}
	r := httptest.NewRequest("GET", "https://nas.test"+usbConsoleAPI+"device/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "nas-session"})
	r.AddCookie(&http.Cookie{Name: consoleCookieName("device", "nas-session"), Value: "device-session"})
	r.Header.Set("Authorization", "Bearer must-not-forward")
	w := httptest.NewRecorder()
	a.nassimhubUSBConsole(w, r)
	if w.Code != 200 || f.called != 1 {
		t.Fatal(w.Code)
	}
	if f.last.Header.Get("Cookie") != "__Host-nsh-admin=device-session" || f.last.Header.Get("Authorization") != "" || strings.Contains(f.last.Header.Get("Cookie"), "nas-session") {
		t.Fatal("NAS credentials forwarded")
	}
	if !strings.Contains(w.Body.String(), `content="/sim-nodes/setting/device"`) || !strings.Contains(w.Body.String(), `src="/sim-nodes/setting/device/admin/app.js"`) {
		t.Fatal("proxy resource paths lost")
	}
	c := w.Result().Cookies()[0]
	if c.Name != "__Secure-ash-"+strings.TrimPrefix(consoleCookieName("device", "nas-session"), "__Secure-ash-") || c.Path != "/sim-nodes/setting/device/" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	if c.Name == consoleCookieName("device", "another-nas-session") || c.Name == consoleCookieName("other-device", "nas-session") {
		t.Fatal("device or NAS session collision")
	}
}
func TestUSBConsoleRejectsOriginsTraversalAndOversize(t *testing.T) {
	f := &usbTestFleet{}
	a := &API{nodeFleet: f}
	for _, x := range []struct{ method, path, body, origin string }{{"GET", usbConsoleAPI + "device/admin/../v1/status", "", ""}, {"GET", usbConsoleAPI + "device/v1/status", "", ""}, {"POST", usbConsoleAPI + "device/admin/login", "{}", "https://attacker.test"}, {"POST", usbConsoleAPI + "device/admin/login", strings.Repeat("x", 65537), "https://nas.test"}} {
		r := httptest.NewRequest(x.method, "https://nas.test"+x.path, strings.NewReader(x.body))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "nas-session"})
		r.Header.Set("Origin", x.origin)
		w := httptest.NewRecorder()
		a.nassimhubUSBConsole(w, r)
		if w.Code < 400 {
			t.Fatal("invalid proxy request accepted")
		}
	}
	if f.called != 0 {
		t.Fatal("invalid request reached device")
	}
}
func TestUSBConsoleRequiresNASAdminAndCSRF(t *testing.T) {
	h := newAdvHarness(t)
	path := usbConsoleAPI + "device/admin/login"
	for _, c := range []*advClient{h.anonymous(), h.aliceClient()} {
		w := c.send("POST", path, "{}")
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("unauthorized USB proxy", w.Code)
		}
	}
	admin := h.admin()
	w := admin.do(advRequest{method: "POST", path: path, body: "{}", noCSRFHeader: true})
	if w.Code != 403 || advErrorCode(w) != "csrf_failed" {
		t.Fatal("missing NAS CSRF accepted", w.Code)
	}
}
