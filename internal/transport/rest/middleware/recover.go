package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
)

// Recover перехватывает паники в обработчиках, записывает детали
// и стек вызовов в лог, а клиенту возвращает общий ответ 500.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel panic value, а не error
					panic(rec)
				}
				log.ErrorContext(r.Context(), "panic in handler",
					"request_id", RequestIDFrom(r.Context()),
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				response.Error(w, http.StatusInternalServerError, response.CodeInternal, "internal server error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
