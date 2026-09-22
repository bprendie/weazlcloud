package desk

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/users"
)

func apiUsersError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	if err == users.ErrUserExists || err == users.ErrBadUsername {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
