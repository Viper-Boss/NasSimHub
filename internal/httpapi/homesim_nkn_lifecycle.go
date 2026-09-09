package httpapi

import "context"

// Restore opt-in services without rewriting revisions or enabling new services.
func (api *API) StartHomeSIMNKNOptionalServices(ctx context.Context) {
	api.initNKNChatStore()
	go func() {
		if settings, err := api.nknNode.loadSettings(); err == nil && settings.Enabled {
			if err := api.nknNode.start(); err != nil {
				api.nknNode.setError(err.Error())
			}
		}
		if settings, err := api.nknNetwork.loadSettings(); err == nil && settings.Enabled {
			api.nknNetwork.startWorker(api)
		}
		<-ctx.Done()
		api.nknNetwork.stopWorker()
		api.nknNode.stop()
	}()
}
