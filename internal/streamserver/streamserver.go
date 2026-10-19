// Package streamserver — учебный стрим-сервер. Он проверяет подпись ссылки, выданной API,
// и отдаёт HLS-плейлист и сегменты, которые готовит Source (в cmd/stream это ffmpeg).
package streamserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/infra/stream"
)

// maxPlaylistSize ограничивает размер читаемого плейлиста; настоящий занимает сотни байт.
const maxPlaylistSize = 1 << 20

// Signer проверяет ссылки и подписывает сегменты плейлиста.
type Signer interface {
	VerifyLink(rawURL string) (stream.Claims, error)
	SignUntil(rawURL string, uid int64, expiresAt time.Time) (string, error)
}

// NewHandler возвращает обработчик /live/..., доступный только по валидной подписи.
//
// Плеер запрашивает сегменты по относительным ссылкам без query, поэтому в отдаваемом
// плейлисте каждый сегмент получает собственную подпись с тем же пользователем и сроком,
// что у плейлиста. Когда срок ссылки истекает, перестаёт работать и обновление плейлиста:
// клиент должен запросить у API новую ссылку.
func NewHandler(log *slog.Logger, signer Signer, src Source) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live/{path...}", func(w http.ResponseWriter, r *http.Request) {
		// В запросе сервера URL содержит только путь и query, а подписан именно путь.
		claims, err := signer.VerifyLink(r.URL.String())
		if err != nil {
			// Подпись и query в лог не пишем: путь и причина достаточны.
			reason := "invalid signature"
			if errors.Is(err, stream.ErrExpired) {
				reason = "link expired"
			}
			log.Info("stream denied", "path", r.URL.Path, "reason", reason)
			http.Error(w, reason, http.StatusForbidden)
			return
		}

		ext := path.Ext(r.URL.Path)
		if ext != ".m3u8" && ext != ".ts" {
			http.NotFound(w, r)
			return
		}
		rc, err := src.Open(r.Context(), r.URL.Path)
		switch {
		case errors.Is(err, ErrNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, ErrUnavailable):
			w.Header().Set("Retry-After", "1")
			http.Error(w, "stream is starting, retry", http.StatusServiceUnavailable)
			return
		case err != nil:
			if r.Context().Err() == nil { // клиент ушёл — не ошибка сервера
				log.Error("open stream", "path", r.URL.Path, "err", err)
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer func() { _ = rc.Close() }()

		// Ссылка временная, поэтому ответ не должен оседать в общих кэшах.
		w.Header().Set("Cache-Control", "no-store")
		log.Debug("stream served", "path", r.URL.Path, "uid", claims.UID)

		if ext == ".ts" {
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = io.Copy(w, rc)
			return
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxPlaylistSize))
		if err == nil {
			body, err = signSegments(signer, r.URL.Path, claims, body)
		}
		if err != nil {
			log.Error("prepare playlist", "path", r.URL.Path, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body) //nolint:gosec // это HLS-плейлист, а не HTML
	})
	return allowAnyOrigin(mux)
}

// allowAnyOrigin разрешает веб-плееру (hls.js) со страницы на другом origin читать плейлист
// и сегменты. Доступ даёт подпись в ссылке, а не cookie или заголовки, поэтому звёздочка
// не открывает ничего лишнего: без валидной подписи ответ всё равно 403.
func allowAnyOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

// signSegments дописывает к каждому URI сегмента подпись, действительную до claims.Expires.
// Строки-теги (#...) и пустые строки остаются как есть.
func signSegments(signer Signer, playlistPath string, claims stream.Claims, playlist []byte) ([]byte, error) {
	dir := path.Dir(playlistPath)
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		uri := strings.TrimSpace(line)
		if uri == "" || strings.HasPrefix(uri, "#") {
			continue
		}
		signed, err := signer.SignUntil(dir+"/"+uri, claims.UID, claims.Expires)
		if err != nil {
			return nil, err
		}
		u, err := url.Parse(signed)
		if err != nil {
			return nil, err
		}
		lines[i] = uri + "?" + u.RawQuery
	}
	return []byte(strings.Join(lines, "\n")), nil
}
