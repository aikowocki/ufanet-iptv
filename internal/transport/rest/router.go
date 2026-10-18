package rest

import (
	"log/slog"
	"net/http"

	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/middleware"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
)

// NewRouter собирает корневой обработчик сервиса с общими middleware.
func NewRouter(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	// Порядок: RequestID -> AccessLog -> Recover, чтобы ответ 500 после паники
	// тоже попал в access-лог с идентификатором запроса.
	return middleware.Chain(jsonErrors(mux),
		middleware.RequestID,
		middleware.AccessLog(log),
		middleware.Recover(log),
	)
}

// jsonErrors заменяет встроенные текстовые ответы ServeMux (404 и 405)
// ответами в едином JSON-формате.
func jsonErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			h.ServeHTTP(w, r)
			return
		}

		// Маршрут не найден: узнаём у mux, что именно он ответил бы (404 или 405).
		probe := &probeWriter{header: http.Header{}}
		h.ServeHTTP(probe, r)

		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		if probe.status == http.StatusMethodNotAllowed {
			response.Error(w, http.StatusMethodNotAllowed, response.CodeMethodNotAllowed, "method not allowed")
			return
		}
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "resource not found")
	})
}

// probeWriter перехватывает ответ стандартного обработчика, не отправляя его клиенту.
type probeWriter struct {
	header http.Header
	status int
}

func (p *probeWriter) Header() http.Header         { return p.header }
func (p *probeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p *probeWriter) WriteHeader(code int)        { p.status = code }

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
