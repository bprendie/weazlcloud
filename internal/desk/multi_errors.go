package desk

import (
	"errors"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/users"
)

func apiUsersError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	if errors.Is(err, users.ErrInsufficientScope) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error(), "code": "insufficient_scope"})
		return
	}
	if err == users.ErrUserExists || err == users.ErrBadUsername {
		status = http.StatusBadRequest
	}
	body := map[string]string{"error": err.Error()}
	if status == http.StatusUnauthorized {
		body["code"] = "authentication_required"
	}
	writeJSON(w, status, body)
}
