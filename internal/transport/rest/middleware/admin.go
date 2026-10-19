package middleware

import (
	"net/http"

	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
)

// RequireAdmin пропускает только администраторов. Ставится после BasicAuth:
// без пользователя в контексте (ошибка сборки маршрута) отвечает 403, а не пропускает запрос.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := UserFrom(r.Context()); !ok || !user.IsAdmin {
			response.Error(w, http.StatusForbidden, response.CodeForbidden, "administrator access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
