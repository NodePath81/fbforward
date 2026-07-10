package flowcontext

import (
	"crypto/subtle"
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

func tokenMatches(r *http.Request, expected string) bool {
	token, ok := bearerToken(r)
	if !ok || expected == "" || len(token) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}
