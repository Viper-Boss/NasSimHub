package localadmin

import (
	"context"
	"github.com/human-agent65535/nassimhub-node/agent/netbackend/radios"
	"net/http"
)

type RadioController interface {
	Status(context.Context) (radios.Status, error)
	Execute(context.Context, radios.Request) (radios.Status, error)
}

func (a *Admin) radioSource() RadioController {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sources.Radios
}
func (a *Admin) radioStatus(w http.ResponseWriter, r *http.Request, _ string, _ http.Handler) {
	s := a.radioSource()
	if s == nil {
		failCode(w, 501, "not_supported", "此设备未安装无线设备管理")
		return
	}
	v, e := s.Status(r.Context())
	if e != nil {
		failCode(w, 503, "unavailable", "无法读取无线设备状态")
		return
	}
	respond(w, 200, v)
}
func (a *Admin) radioChange(w http.ResponseWriter, r *http.Request, _ string, _ http.Handler) {
	var q radios.Request
	if !decode(w, r, &q) {
		return
	}
	if e := radios.Validate(q); e != nil {
		fail(w, 400, e.Error())
		return
	}
	s := a.radioSource()
	if s == nil {
		failCode(w, 501, "not_supported", "此设备未安装无线设备管理")
		return
	}
	v, e := s.Execute(r.Context(), q)
	if e != nil {
		failCode(w, 409, "radio_operation_failed", e.Error())
		return
	}
	respond(w, 200, v)
}
