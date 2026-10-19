// Package invalidate превращает события об изменении данных в сброс локальных кэшей.
package invalidate

import "github.com/aikowocki/ufanet-iptv/internal/domain"

// Users — кэш, зависящий от состояния пользователей (проверки паролей).
type Users interface {
	InvalidateUser(id int64)
	InvalidateAll()
}

// Catalog — кэш списков каналов.
type Catalog interface {
	InvalidateCatalog()
}

// Handler применяет события, пришедшие от других экземпляров (и от этого же), к локальным кэшам.
type Handler struct {
	users   Users
	catalog Catalog
}

// New создаёт Handler.
func New(users Users, catalog Catalog) *Handler {
	return &Handler{users: users, catalog: catalog}
}

// Handle сбрасывает то, что затрагивает событие. Неизвестный вид события сбрасывает всё:
// лишний сброс безвреден, а пропущенный оставил бы устаревшие данные до TTL.
func (h *Handler) Handle(ev domain.InvalidationEvent) {
	switch ev.Kind {
	case domain.InvalidateUser:
		h.users.InvalidateUser(ev.ID)
	case domain.InvalidateChannel, domain.InvalidatePackage, domain.InvalidateSubscription:
		h.catalog.InvalidateCatalog()
	default:
		h.Reset()
	}
}

// Reset сбрасывает все кэши целиком: после (пере)подключения к LISTEN события могли быть потеряны.
func (h *Handler) Reset() {
	h.users.InvalidateAll()
	h.catalog.InvalidateCatalog()
}
