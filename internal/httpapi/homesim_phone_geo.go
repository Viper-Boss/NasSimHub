package httpapi

import (
	"net/http"

	"github.com/human-agent65535/modemdeck/internal/phonegeo"
)

// homeSIMPhoneGeo resolves a mainland China mobile number to its offline
// region and carrier record. The dataset is embedded in the binary so the
// endpoint works fully offline.
func (api *API) homeSIMPhoneGeo(response http.ResponseWriter, request *http.Request) {
	number := request.URL.Query().Get("number")
	record, ok := phonegeo.Lookup(number)
	if !ok {
		writeError(response, 404, "not_found", "No offline region record for this number prefix", "number")
		return
	}
	writeJSON(response, 200, map[string]any{
		"number":       number,
		"province":     record.Province,
		"city":         record.City,
		"zip_code":     record.ZipCode,
		"area_zone":    record.AreaZone,
		"carrier":      record.Carrier,
		"data_version": phonegeo.Version(),
	})
}
