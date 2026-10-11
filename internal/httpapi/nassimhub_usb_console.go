package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const usbConsoleAPI = "/api/v1/nodes/usb-console/"
const usbConsolePage = "/sim-nodes/setting/"

var consoleID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type usbConsoleService interface {
	USBAdministration(context.Context, string, *http.Request) (*http.Response, error)
}

func consoleCookieName(id, session string) string {
	h := sha256.Sum256([]byte(id + "\x00" + session))
	return "__Secure-ash-" + hex.EncodeToString(h[:12])
}
func (api *API) nassimhubUSBConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	service, ok := api.nodeFleet.(usbConsoleService)
	if !ok {
		writeError(w, 503, "usb_console_unavailable", "USB console is unavailable", "")
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, usbConsoleAPI), "/", 2)
	if len(parts) != 2 || !consoleID.MatchString(parts[0]) || r.URL.RawPath != "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	p := "/" + parts[1]
	if path.Clean(p) != p || (p != "/" && !strings.HasPrefix(p, "/admin/")) || len(r.URL.RawQuery) > 4096 {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" && r.Method != "DELETE" && r.Method != "PUT" {
		w.Header().Set("Allow", "GET, HEAD, POST, PUT, DELETE")
		writeError(w, 405, "method_not_allowed", "Unsupported console method", "")
		return
	}
	session, e := r.Cookie(sessionCookieName)
	if e != nil || session.Value == "" {
		writeError(w, 401, "authentication_required", "NAS login required", "")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		origin, e := url.Parse(r.Header.Get("Origin"))
		if e != nil || origin.Scheme != "https" || origin.Host != r.Host || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			writeError(w, 403, "invalid_origin", "Invalid console request origin", "")
			return
		}
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if e != nil {
		writeError(w, 413, "request_too_large", "Console request too large", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	upstream, e := http.NewRequestWithContext(ctx, r.Method, "https://10.55.0.2:7581"+p, bytes.NewReader(body))
	if e != nil {
		writeError(w, 400, "invalid_request", "Invalid console request", "")
		return
	}
	upstream.URL.RawQuery = r.URL.RawQuery
	for _, h := range []string{"Content-Type", "X-CSRF-Token", "X-NSH-Passive"} {
		if v := r.Header.Get(h); v != "" {
			upstream.Header.Set(h, v)
		}
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		upstream.Header.Set("Origin", "https://10.55.0.2:7581")
	}
	cookieName := consoleCookieName(id, session.Value)
	if c, e := r.Cookie(cookieName); e == nil {
		upstream.AddCookie(&http.Cookie{Name: "__Host-nsh-admin", Value: c.Value})
	}
	response, e := service.USBAdministration(ctx, id, upstream)
	if e != nil {
		writeError(w, 502, "usb_console_unavailable", "USB console offline or device identity could not be verified", "")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		writeError(w, 502, "invalid_device_response", "Device redirects are not allowed", "")
		return
	}
	payload, e := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if e != nil || len(payload) > 16<<20 {
		writeError(w, 502, "invalid_device_response", "Device response unavailable", "")
		return
	}
	base := usbConsolePage + id
	for _, c := range response.Cookies() {
		if c.Name != "__Host-nsh-admin" {
			continue
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: c.Value, Path: base + "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: c.MaxAge, Expires: c.Expires})
	}
	for _, h := range []string{"Content-Type", "Content-Disposition"} {
		if v := response.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		nasCSRF := ""
		if c, e := r.Cookie(csrfCookieName); e == nil {
			nasCSRF = c.Value
		}
		text := string(payload)
		text = strings.Replace(text, "</head>", "<meta name=\"airsimhub-nas-csrf\" content=\""+html.EscapeString(nasCSRF)+"\"></head>", 1)
		text = strings.Replace(text, "</head>", "<meta name=\"airsimhub-proxy-base\" content=\""+html.EscapeString(base)+"\"></head>", 1)
		text = strings.ReplaceAll(text, "href=\"/admin/", "href=\""+base+"/admin/")
		text = strings.ReplaceAll(text, "src=\"/admin/", "src=\""+base+"/admin/")
		text = strings.Replace(text, "class=\"brand\" href=\"/\"", "class=\"brand\" href=\""+base+"/\"", 1)
		payload = []byte(text)
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != "HEAD" {
		_, _ = w.Write(payload)
	}
}
