package flowcontext

import (
	"net/http"
	"strings"
)

func bearerToken(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	const prefix = "Bearer "
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	return token, token != ""
}
