// Package response содержит помощники для ответов API в едином формате.
// Вынесен отдельно, чтобы им могли пользоваться и handlers, и middleware.
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Коды ошибок в теле ответа. Клиент ориентируется на них, а не на текст.
const (
	CodeBadRequest       = "bad_request"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternal         = "internal_error"
	CodeUnavailable      = "service_unavailable"
)

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JSON сериализует v и отправляет ответ с указанным статусом.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Заголовки уже отправлены, остаётся только записать в лог.
		slog.Warn("write response", "error", err)
	}
}

// Error отправляет ошибку в едином формате:
// {"error":{"code":"...","message":"..."}}
// message предназначен для клиента и не должен содержать внутренних деталей.
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, errorBody{Error: errorPayload{Code: code, Message: message}})
}
