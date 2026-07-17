package control

import (
	"errors"
	"net/http"
	"strings"

	"github.com/duvu/ya-router/internal/secret"
)

// SecretMetadataReader exposes only redacted secret posture. Implementations
// must never return secret values through this interface.
type SecretController interface {
	List() []secret.Metadata
	Set(actor, id, value string) (secret.Metadata, error)
	Delete(actor, id string) error
}

type secretWriteRequest struct {
	Slot  string  `json:"slot"`
	Value *string `json:"value"`
}

type secretDeleteRequest struct {
	Slot string `json:"slot"`
}

// RegisterSecretRoutes adds the redacted secret-metadata read route. Values are
// never returned: the control plane exposes only configured/source/read-only
// posture so clients can display credential state without ever receiving a
// secret. Viewer role suffices because no value is disclosed.
func RegisterSecretRoutes(api *API, controller SecretController) {
	if controller == nil {
		return
	}
	api.Handle(http.MethodGet, "/control/v1/secrets", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		metadata := controller.List()
		if metadata == nil {
			metadata = []secret.Metadata{}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": metadata})
	}))
	api.Handle(http.MethodPut, "/control/v1/secrets", RoleOperator, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeSecret(writer, request, controller)
	}))
	api.Handle(http.MethodDelete, "/control/v1/secrets", RoleOperator, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		deleteSecret(writer, request, controller)
	}))
}

func writeSecret(writer http.ResponseWriter, request *http.Request, controller SecretController) {
	var input secretWriteRequest
	if err := decodeStrictJSON(request, &input); err != nil || input.Value == nil || strings.TrimSpace(*input.Value) == "" || !validSecretSlot(input.Slot) {
		writeError(writer, request, http.StatusBadRequest, "invalid_secret_write", "The secret write request is invalid.", false, nil)
		return
	}
	identity, _ := IdentityFromContext(request.Context())
	metadata, err := controller.Set(identity.Subject, strings.TrimSpace(input.Slot), *input.Value)
	if err != nil {
		writeSecretError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, metadata)
}

func deleteSecret(writer http.ResponseWriter, request *http.Request, controller SecretController) {
	var input secretDeleteRequest
	if err := decodeStrictJSON(request, &input); err != nil || !validSecretSlot(input.Slot) {
		writeError(writer, request, http.StatusBadRequest, "invalid_secret_delete", "The secret delete request is invalid.", false, nil)
		return
	}
	identity, _ := IdentityFromContext(request.Context())
	if err := controller.Delete(identity.Subject, strings.TrimSpace(input.Slot)); err != nil {
		writeSecretError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func validSecretSlot(slot string) bool {
	slot = strings.TrimSpace(slot)
	if slot == "" || len(slot) > 128 {
		return false
	}
	for _, character := range slot {
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && character != '/' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func writeSecretError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, secret.ErrReadOnly):
		writeError(writer, request, http.StatusConflict, "secret_read_only", "The secret is supplied by a read-only source.", false, nil)
	case errors.Is(err, secret.ErrNotFound):
		writeError(writer, request, http.StatusNotFound, "secret_not_found", "The secret was not found.", false, nil)
	default:
		writeError(writer, request, http.StatusServiceUnavailable, "secret_write_failed", "The secret could not be updated.", true, nil)
	}
}
