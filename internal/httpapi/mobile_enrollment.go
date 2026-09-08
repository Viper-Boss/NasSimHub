package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/mobilepairing"
)

type mobileInvite struct {
	UserID  string
	Expires time.Time
}

func (api *API) createMobileInvite(w http.ResponseWriter, r *http.Request) {
	if _, mobile := mobileAuthenticationFromContext(r.Context()); mobile {
		writeError(w, 403, "browser_required", "请在已登录的网页中创建配对码", "")
		return
	}
	principal, exists := auth.PrincipalFromContext(r.Context())
	if !exists || !principal.IOSPairingEnabled {
		writeError(w, 403, "pairing_not_allowed", "请先为当前账户启用手机配对权限", "")
		return
	}
	var input struct {
		ServerURL string `json:"server_url"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	u, err := url.Parse(strings.TrimSpace(input.ServerURL))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		writeError(w, 422, "invalid_server_url", "请填写手机可访问的 HTTPS 服务器地址，不含路径、账号或参数", "server_url")
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		api.writeInternalError(w, r, "create mobile invitation", err)
		return
	}
	invitation := base64.RawURLEncoding.EncodeToString(random[:])
	digest := sha256.Sum256([]byte(invitation))
	expires := time.Now().UTC().Add(5 * time.Minute)
	api.mobileInviteMu.Lock()
	if api.mobileInvites == nil {
		api.mobileInvites = map[[32]byte]mobileInvite{}
	}
	for key, value := range api.mobileInvites {
		if value.UserID == principal.UserID || time.Now().After(value.Expires) {
			delete(api.mobileInvites, key)
		}
	}
	if len(api.mobileInvites) >= 128 {
		api.mobileInviteMu.Unlock()
		writeError(w, 429, "pairing_busy", "配对请求较多，请稍后重试", "")
		return
	}
	api.mobileInvites[digest] = mobileInvite{UserID: principal.UserID, Expires: expires}
	api.mobileInviteMu.Unlock()
	api.homeSIMNKNStatusMu.Lock()
	nknStatus := api.homeSIMNKNStatus
	api.homeSIMNKNStatusMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"payload": map[string]any{
		"type": "nassimhub.mobile.enrollment", "version": 1,
		"server_url": strings.TrimRight(u.String(), "/"), "api_base": "/api/v1", "invitation": invitation,
		"expires_at": expires, "exchange_path": "/api/v1/mobile/enrollment/exchange",
		"key_exchange": "RSA-OAEP-256", "nkn_address": nknStatus.Address,
		"nkn_http_tunnel": false,
	}})
}

func (api *API) exchangeMobileInvite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Invitation string `json:"invitation"`
		PublicKey  string `json:"public_key"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if len(input.Invitation) != 43 || len(input.PublicKey) > 8192 {
		writeError(w, 400, "invalid_pairing", "配对信息无效", "")
		return
	}
	block, rest := pem.Decode([]byte(input.PublicKey))
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) > 0 {
		writeError(w, 400, "invalid_public_key", "需要 RSA 公钥（SPKI PEM）", "public_key")
		return
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	publicKey, ok := parsed.(*rsa.PublicKey)
	if err != nil || !ok || publicKey.N.BitLen() < 2048 || publicKey.N.BitLen() > 4096 || publicKey.E != 65537 {
		writeError(w, 400, "invalid_public_key", "需要 2048–4096 位 RSA 公钥", "public_key")
		return
	}
	digest := sha256.Sum256([]byte(input.Invitation))
	api.mobileInviteMu.Lock()
	defer api.mobileInviteMu.Unlock()
	invite, exists := api.mobileInvites[digest]
	if !exists || !time.Now().Before(invite.Expires) {
		delete(api.mobileInvites, digest)
		writeError(w, 401, "invitation_expired", "二维码已失效，请重新生成", "")
		return
	}
	repository, ok := api.repository.(mobilePairingRepository)
	if !ok {
		writeError(w, 503, "pairing_unavailable", "手机配对服务暂不可用", "")
		return
	}
	status, err := repository.IOSPairingStatus(r.Context(), invite.UserID)
	if err != nil {
		api.writeInternalError(w, r, "read mobile permission", err)
		return
	}
	if !status.Allowed {
		delete(api.mobileInvites, digest)
		writeError(w, 403, "pairing_not_allowed", "手机配对权限已关闭", "")
		return
	}
	token, tokenDigest, err := mobilepairing.NewToken()
	if err != nil {
		api.writeInternalError(w, r, "generate mobile credential", err)
		return
	}
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, []byte(token), []byte("nassimhub.mobile.enrollment.v1"))
	if err != nil {
		api.writeInternalError(w, r, "encrypt mobile credential", err)
		return
	}
	if _, err = repository.CreateIOSPairingCredential(r.Context(), invite.UserID, tokenDigest); err != nil {
		api.writeMobilePairingError(w, r, "create mobile credential", err)
		return
	}
	delete(api.mobileInvites, digest)
	writeJSON(w, 201, map[string]any{"algorithm": "RSA-OAEP-256", "encrypted_token": base64.StdEncoding.EncodeToString(ciphertext), "label": "nassimhub.mobile.enrollment.v1", "token_type": "Bearer", "api_base": "/api/v1"})
}
