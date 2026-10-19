package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/middleware"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/admin"
)

// maxAdminBody ограничивает тело админского запроса.
const maxAdminBody = 64 << 10

// AdminService — управление данными сервиса. Реализует usecase/admin.Service.
type AdminService interface {
	ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error)
	GetUser(ctx context.Context, id int64) (domain.User, error)
	CreateUser(ctx context.Context, in admin.UserInput) (domain.User, error)
	UpdateUser(ctx context.Context, actor domain.User, id int64, in admin.UserUpdate) (domain.User, error)

	ListChannels(ctx context.Context) ([]domain.Channel, error)
	CreateChannel(ctx context.Context, in admin.ChannelInput) (domain.Channel, error)
	UpdateChannel(ctx context.Context, id int64, p admin.ChannelPatch) (domain.Channel, error)

	ListPackages(ctx context.Context) ([]domain.Package, error)
	CreatePackage(ctx context.Context, in admin.PackageInput) (domain.Package, error)
	UpdatePackage(ctx context.Context, id int64, p admin.PackagePatch) (domain.Package, error)
	SetPackageChannels(ctx context.Context, packageID int64, channelIDs []int64) (domain.Package, error)

	ListUserSubscriptions(ctx context.Context, userID int64) ([]domain.UserSubscription, error)
	CreateSubscription(ctx context.Context, userID int64, in admin.SubscriptionInput) (domain.UserSubscription, error)
	UpdateSubscription(ctx context.Context, id int64, p admin.SubscriptionPatch) (domain.UserSubscription, error)
}

// Ответы админки. Хэш пароля в DTO отсутствует намеренно.
type (
	adminUserDTO struct {
		ID       int64  `json:"id"`
		Login    string `json:"login"`
		IsActive bool   `json:"is_active"`
		IsAdmin  bool   `json:"is_admin"`
	}
	adminChannelDTO struct {
		ID         int64  `json:"id"`
		Number     int    `json:"number"`
		Name       string `json:"name"`
		StreamPath string `json:"stream_path"`
		IsActive   bool   `json:"is_active"`
	}
	adminPackageDTO struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		IsActive   bool    `json:"is_active"`
		ChannelIDs []int64 `json:"channel_ids"`
	}
	adminSubscriptionDTO struct {
		ID        int64      `json:"id"`
		UserID    int64      `json:"user_id"`
		PackageID int64      `json:"package_id"`
		IsActive  bool       `json:"is_active"`
		ExpiresAt *time.Time `json:"expires_at"`
		CreatedAt time.Time  `json:"created_at"`
	}
)

func userDTO(u domain.User) adminUserDTO {
	return adminUserDTO{ID: u.ID, Login: u.Login, IsActive: u.IsActive, IsAdmin: u.IsAdmin}
}

func channelDTOAdmin(c domain.Channel) adminChannelDTO {
	return adminChannelDTO{ID: c.ID, Number: c.Number, Name: c.Name, StreamPath: c.StreamPath, IsActive: c.IsActive}
}

func packageDTO(p domain.Package) adminPackageDTO {
	ids := p.ChannelIDs
	if ids == nil {
		ids = []int64{}
	}
	return adminPackageDTO{ID: p.ID, Name: p.Name, IsActive: p.IsActive, ChannelIDs: ids}
}

func subscriptionDTOAdmin(s domain.UserSubscription) adminSubscriptionDTO {
	return adminSubscriptionDTO{ID: s.ID, UserID: s.UserID, PackageID: s.PackageID,
		IsActive: s.IsActive, ExpiresAt: s.ExpiresAt, CreatedAt: s.CreatedAt}
}

// optionalTime отличает отсутствующее поле от null: null сбрасывает срок (подписка становится бессрочной).
type optionalTime struct {
	domain.OptionalTime
}

func (o *optionalTime) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var t time.Time
	if err := json.Unmarshal(b, &t); err != nil {
		return errors.New("expires_at must be an RFC 3339 timestamp or null")
	}
	o.Value = &t
	return nil
}

// Запросы админки.
type (
	createUserReq struct {
		Login    string `json:"login"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
		IsActive *bool  `json:"is_active"`
	}
	updateUserReq struct {
		IsActive *bool   `json:"is_active"`
		IsAdmin  *bool   `json:"is_admin"`
		Password *string `json:"password"`
	}
	createChannelReq struct {
		Number     int    `json:"number"`
		Name       string `json:"name"`
		StreamPath string `json:"stream_path"`
		IsActive   *bool  `json:"is_active"`
	}
	updateChannelReq struct {
		Number     *int    `json:"number"`
		Name       *string `json:"name"`
		StreamPath *string `json:"stream_path"`
		IsActive   *bool   `json:"is_active"`
	}
	createPackageReq struct {
		Name     string `json:"name"`
		IsActive *bool  `json:"is_active"`
	}
	updatePackageReq struct {
		Name     *string `json:"name"`
		IsActive *bool   `json:"is_active"`
	}
	setPackageChannelsReq struct {
		ChannelIDs *[]int64 `json:"channel_ids"`
	}
	createSubscriptionReq struct {
		PackageID int64        `json:"package_id"`
		IsActive  *bool        `json:"is_active"`
		ExpiresAt optionalTime `json:"expires_at"`
	}
	updateSubscriptionReq struct {
		IsActive  *bool        `json:"is_active"`
		ExpiresAt optionalTime `json:"expires_at"`
	}
)

func (a api) registerAdmin(mux *http.ServeMux) {
	auth := middleware.BasicAuth(a.log, a.deps.Auth, authRealm)
	h := func(f http.HandlerFunc) http.Handler { return auth(middleware.RequireAdmin(f)) }
	const p = "/api/v1/admin"

	mux.Handle("GET "+p+"/users", h(a.adminListUsers))
	mux.Handle("POST "+p+"/users", h(a.adminCreateUser))
	mux.Handle("GET "+p+"/users/{id}", h(a.adminGetUser))
	mux.Handle("PATCH "+p+"/users/{id}", h(a.adminUpdateUser))

	mux.Handle("GET "+p+"/channels", h(a.adminListChannels))
	mux.Handle("POST "+p+"/channels", h(a.adminCreateChannel))
	mux.Handle("PATCH "+p+"/channels/{id}", h(a.adminUpdateChannel))

	mux.Handle("GET "+p+"/packages", h(a.adminListPackages))
	mux.Handle("POST "+p+"/packages", h(a.adminCreatePackage))
	mux.Handle("PATCH "+p+"/packages/{id}", h(a.adminUpdatePackage))
	mux.Handle("PUT "+p+"/packages/{id}/channels", h(a.adminSetPackageChannels))

	mux.Handle("GET "+p+"/users/{id}/subscriptions", h(a.adminListSubscriptions))
	mux.Handle("POST "+p+"/users/{id}/subscriptions", h(a.adminCreateSubscription))
	mux.Handle("PATCH "+p+"/subscriptions/{id}", h(a.adminUpdateSubscription))
}

func (a api) adminListUsers(w http.ResponseWriter, r *http.Request) {
	limit, ok1 := queryInt(w, r, "limit")
	offset, ok2 := queryInt(w, r, "offset")
	if !ok1 || !ok2 {
		return
	}
	list, err := a.deps.Admin.ListUsers(r.Context(), limit, offset)
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	out := make([]adminUserDTO, 0, len(list))
	for _, u := range list {
		out = append(out, userDTO(u))
	}
	response.JSON(w, http.StatusOK, map[string]any{"users": out})
}

func (a api) adminGetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := a.deps.Admin.GetUser(r.Context(), id)
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, userDTO(u))
}

func (a api) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if !decodeBody(w, r, &req) {
		return
	}
	u, err := a.deps.Admin.CreateUser(r.Context(), admin.UserInput{
		Login: req.Login, Password: req.Password, IsAdmin: req.IsAdmin, IsActive: req.IsActive,
	})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, userDTO(u))
}

func (a api) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updateUserReq
	if !decodeBody(w, r, &req) {
		return
	}
	actor, _ := middleware.UserFrom(r.Context())
	u, err := a.deps.Admin.UpdateUser(r.Context(), actor, id, admin.UserUpdate{
		IsActive: req.IsActive, IsAdmin: req.IsAdmin, Password: req.Password,
	})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, userDTO(u))
}

func (a api) adminListChannels(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Admin.ListChannels(r.Context())
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	out := make([]adminChannelDTO, 0, len(list))
	for _, c := range list {
		out = append(out, channelDTOAdmin(c))
	}
	response.JSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (a api) adminCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req createChannelReq
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := a.deps.Admin.CreateChannel(r.Context(), admin.ChannelInput{
		Number: req.Number, Name: req.Name, StreamPath: req.StreamPath, IsActive: req.IsActive,
	})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, channelDTOAdmin(c))
}

func (a api) adminUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updateChannelReq
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := a.deps.Admin.UpdateChannel(r.Context(), id, admin.ChannelPatch{
		Number: req.Number, Name: req.Name, StreamPath: req.StreamPath, IsActive: req.IsActive,
	})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, channelDTOAdmin(c))
}

func (a api) adminListPackages(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Admin.ListPackages(r.Context())
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	out := make([]adminPackageDTO, 0, len(list))
	for _, p := range list {
		out = append(out, packageDTO(p))
	}
	response.JSON(w, http.StatusOK, map[string]any{"packages": out})
}

func (a api) adminCreatePackage(w http.ResponseWriter, r *http.Request) {
	var req createPackageReq
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := a.deps.Admin.CreatePackage(r.Context(), admin.PackageInput{Name: req.Name, IsActive: req.IsActive})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, packageDTO(p))
}

func (a api) adminUpdatePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updatePackageReq
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := a.deps.Admin.UpdatePackage(r.Context(), id, admin.PackagePatch{Name: req.Name, IsActive: req.IsActive})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, packageDTO(p))
}

func (a api) adminSetPackageChannels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req setPackageChannelsReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.ChannelIDs == nil {
		response.Error(w, http.StatusBadRequest, response.CodeBadRequest, "channel_ids is required")
		return
	}
	p, err := a.deps.Admin.SetPackageChannels(r.Context(), id, *req.ChannelIDs)
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, packageDTO(p))
}

func (a api) adminListSubscriptions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	list, err := a.deps.Admin.ListUserSubscriptions(r.Context(), id)
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	out := make([]adminSubscriptionDTO, 0, len(list))
	for _, s := range list {
		out = append(out, subscriptionDTOAdmin(s))
	}
	response.JSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

func (a api) adminCreateSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req createSubscriptionReq
	if !decodeBody(w, r, &req) {
		return
	}
	s, err := a.deps.Admin.CreateSubscription(r.Context(), id, admin.SubscriptionInput{
		PackageID: req.PackageID, IsActive: req.IsActive, ExpiresAt: req.ExpiresAt.OptionalTime,
	})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, subscriptionDTOAdmin(s))
}

func (a api) adminUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updateSubscriptionReq
	if !decodeBody(w, r, &req) {
		return
	}
	s, err := a.deps.Admin.UpdateSubscription(r.Context(), id, admin.SubscriptionPatch{
		IsActive: req.IsActive, ExpiresAt: req.ExpiresAt.OptionalTime,
	})
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, subscriptionDTOAdmin(s))
}

// adminError переводит доменные ошибки в HTTP-ответы. Неизвестные ошибки попадают в лог,
// клиент видит общий текст.
func (a api) adminError(w http.ResponseWriter, r *http.Request, err error) {
	var verr *domain.ValidationError
	var rerr *domain.ReferenceError
	switch {
	case errors.As(err, &rerr):
		response.Error(w, http.StatusUnprocessableEntity, response.CodeUnprocessable, rerr.Message)
	case errors.As(err, &verr):
		response.Error(w, http.StatusBadRequest, response.CodeBadRequest, verr.Message)
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "resource not found")
	case errors.Is(err, domain.ErrConflict):
		response.Error(w, http.StatusConflict, response.CodeConflict, "resource with these unique values already exists")
	case errors.Is(err, domain.ErrSelfLockout):
		response.Error(w, http.StatusConflict, response.CodeConflict, domain.ErrSelfLockout.Error())
	case errors.Is(err, domain.ErrBusy):
		w.Header().Set("Retry-After", "1")
		response.Error(w, http.StatusServiceUnavailable, response.CodeUnavailable, "service is busy, retry later")
	default:
		a.internalError(w, r, err)
	}
}

// decodeBody читает JSON-тело строго: ограничение размера, неизвестные поля и
// лишние данные после объекта дают 400. Тело не попадает в лог (в нём бывает пароль).
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		// Второй Decode должен вернуть EOF, иначе за объектом есть мусор.
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("unexpected data after JSON object")
		}
	}
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.Error(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, "request body is too large")
		return false
	}
	response.Error(w, http.StatusBadRequest, response.CodeBadRequest, "invalid JSON body: "+safeDecodeMessage(err))
	return false
}

// safeDecodeMessage оставляет для клиента только описание ошибки разбора без значений из тела.
func safeDecodeMessage(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return "syntax error"
	case errors.As(err, &typ):
		return "field " + typ.Field + " has wrong type"
	case errors.Is(err, io.EOF):
		return "body is empty"
	default:
		return err.Error()
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, response.CodeBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// queryInt читает необязательный целый параметр запроса; 0 означает «не задан».
func queryInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		response.Error(w, http.StatusBadRequest, response.CodeBadRequest, name+" must be a non-negative integer")
		return 0, false
	}
	return n, true
}
