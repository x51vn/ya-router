package control

import (
	"net/http"
	"sort"
)

type MetaResponse struct {
	ServiceVersion  string              `json:"service_version"`
	ControlAPIs     []string            `json:"control_api_versions"`
	Features        []string            `json:"features"`
	Client          ClientCompatibility `json:"client_compatibility"`
	DeploymentMode  string              `json:"deployment_mode"`
	ConfigRevision  uint64              `json:"config_revision"`
	RestartRequired bool                `json:"restart_required"`
}

func (api *API) metaHandler(writer http.ResponseWriter, request *http.Request) {
	state := StateMeta{}
	if api.options.State != nil {
		state = api.options.State()
	}
	features := append([]string(nil), api.options.Features...)
	sort.Strings(features)
	writeJSON(writer, http.StatusOK, MetaResponse{
		ServiceVersion:  api.options.ServiceVersion,
		ControlAPIs:     []string{ControlAPIVersion},
		Features:        features,
		Client:          compatibilityFromContext(request.Context()),
		DeploymentMode:  api.options.DeploymentMode,
		ConfigRevision:  state.Revision,
		RestartRequired: state.RestartRequired,
	})
}
