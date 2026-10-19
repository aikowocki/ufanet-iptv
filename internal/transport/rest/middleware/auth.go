package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
)

// Authenticator проверяет учётные данные из Basic Auth.
type Authenticator interface {
	Authenticate(ctx context.Context, login, password string) (domain.User, error)
}

type userKey struct{}

// UserFrom возвращает пользователя, прошедшего BasicAuth.
func UserFrom(ctx context.Context) (domain.User, bool) {
	u, ok := ctx.Value(userKey{}).(domain.User)
	return u, ok
}

// retryAfterBusy — подсказка клиенту при перегрузке проверки паролей (секунды).
const retryAfterBusy = "1"

// BasicAuth требует корректный заголовок Authorization: Basic. При успехе кладёт
// пользователя в контекст запроса. Заголовок и пароль не попадают в лог.
// Ответы: 401 с WWW-Authenticate (нет заголовка или неверные данные),
// 503 с Retry-After при перегрузке, 500 с общим текстом при внутренней ошибке.
func BasicAuth(log *slog.Logger, auth Authenticator, realm string) Middleware {
	challenge := `Basic realm="` + realm + `", charset="UTF-8"`
	unauthorized := func(w http.ResponseWriter) {
		w.Header().Set("WWW-Authenticate", challenge)
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "authentication required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			login, password, ok := r.BasicAuth()
			if !ok || login == "" {
				unauthorized(w)
				return
			}
			user, err := auth.Authenticate(r.Context(), login, password)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
			case errors.Is(err, domain.ErrInvalidCredentials):
				unauthorized(w)
			case errors.Is(err, domain.ErrBusy):
				w.Header().Set("Retry-After", retryAfterBusy)
				response.Error(w, http.StatusServiceUnavailable, response.CodeUnavailable, "service is busy, retry later")
			default:
				log.ErrorContext(r.Context(), "authenticate", "request_id", RequestIDFrom(r.Context()), "error", err)
				response.Error(w, http.StatusInternalServerError, response.CodeInternal, "internal error")
			}
		})
	}
}
