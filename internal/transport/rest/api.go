package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/middleware"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
)

// authRealm показывается клиенту в заголовке WWW-Authenticate.
const authRealm = "iptv"

// ChannelLister возвращает каналы каталога с признаком доступности пользователю.
type ChannelLister interface {
	List(ctx context.Context, user domain.User) ([]domain.ChannelAccess, error)
}

// LinkBuilder возвращает ссылку на поток канала.
type LinkBuilder interface {
	Link(ctx context.Context, user domain.User, channelID int64) (string, error)
}

// Deps — зависимости API абонента. Если Auth равен nil, маршруты /api/v1 не регистрируются.
type Deps struct {
	Auth     middleware.Authenticator
	Channels ChannelLister
	Playback LinkBuilder
}

type channelDTO struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Name   string `json:"name"`
	// Available равен false, если нужна подписка: приставка может предложить её оформить.
	Available bool `json:"available"`
}

type channelsResponse struct {
	Channels []channelDTO `json:"channels"`
}

type playbackResponse struct {
	ChannelID int64  `json:"channel_id"`
	URL       string `json:"url"`
}

type api struct {
	log  *slog.Logger
	deps Deps
}

func (a api) register(mux *http.ServeMux) {
	auth := middleware.BasicAuth(a.log, a.deps.Auth, authRealm)
	mux.Handle("GET /api/v1/channels", auth(http.HandlerFunc(a.handleChannels)))
	mux.Handle("GET /api/v1/channels/{id}/playback", auth(http.HandlerFunc(a.handlePlayback)))
}

func (a api) handleChannels(w http.ResponseWriter, r *http.Request) {
	user, _ := middleware.UserFrom(r.Context())
	list, err := a.deps.Channels.List(r.Context(), user)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	out := channelsResponse{Channels: make([]channelDTO, 0, len(list))}
	for _, c := range list {
		out.Channels = append(out.Channels, channelDTO{ID: c.ID, Number: c.Number, Name: c.Name, Available: c.Available})
	}
	response.JSON(w, http.StatusOK, out)
}

func (a api) handlePlayback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, response.CodeBadRequest, "invalid channel id")
		return
	}
	user, _ := middleware.UserFrom(r.Context())
	url, err := a.deps.Playback.Link(r.Context(), user, id)
	switch {
	case err == nil:
		response.JSON(w, http.StatusOK, playbackResponse{ChannelID: id, URL: url})
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "channel not found")
	case errors.Is(err, domain.ErrForbidden):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "no active subscription for this channel, please subscribe")
	default:
		a.internalError(w, r, err)
	}
}

// internalError пишет причину в лог, а клиенту отдаёт общий текст без деталей.
func (a api) internalError(w http.ResponseWriter, r *http.Request, err error) {
	a.log.ErrorContext(r.Context(), "handler failed",
		"request_id", middleware.RequestIDFrom(r.Context()), "path", r.URL.Path, "error", err)
	response.Error(w, http.StatusInternalServerError, response.CodeInternal, "internal error")
}
