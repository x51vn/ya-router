package control

import (
	"encoding/json"
	"net/http"
)

// MutationExecutor applies a validated, revision-safe mutation and returns a
// redacted result payload. It is implemented by the daemon runtime, which owns
// state and reconcile. The status is a control HTTP status; code is a stable
// machine-readable error code when err is non-nil.
type MutationExecutor interface {
	Execute(request MutationRequest, actor string) (result any, status int, code string, err error)
}

// RegisterMutationRoutes adds the revision-safe configuration mutation route.
// Mutations require the operator role. The daemon executor enforces
// compare-and-swap, validation, and hot reload; this layer only handles
// transport, RBAC, idempotency, and the typed error envelope.
func RegisterMutationRoutes(api *API, executor MutationExecutor) {
	if executor == nil {
		return
	}
	api.Handle(http.MethodPost, "/control/v1/config/mutations", RoleOperator, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var mutation MutationRequest
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&mutation); err != nil {
			writeError(writer, request, http.StatusBadRequest, "invalid_mutation_request", "The mutation request body is invalid.", false, nil)
			return
		}
		identity, _ := IdentityFromContext(request.Context())
		result, status, code, err := executor.Execute(mutation, identity.Subject)
		if err != nil {
			retryable := status == http.StatusConflict
			var details map[string]any
			if detailed, ok := result.(interface{ ErrorDetails() map[string]any }); ok {
				details = detailed.ErrorDetails()
			}
			writeError(writer, request, status, code, err.Error(), retryable, details)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	}))
}
