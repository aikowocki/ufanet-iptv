package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestIDFrom возвращает идентификатор запроса, выставленный middleware RequestID.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// RequestID добавляет каждому запросу уникальный идентификатор,
// сохраняет его в контексте и возвращает клиенту в заголовке X-Request-ID.
// Идентификатор генерируется заново и не берётся из входящего заголовка:
// клиенту нельзя доверять, значение попадает в логи.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()

		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
