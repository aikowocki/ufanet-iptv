// Package domain содержит сущности сервиса и ошибки бизнес-логики.
// Пакет не зависит ни от БД, ни от HTTP.
package domain

import "errors"

// User — абонент или администратор.
type User struct {
	ID           int64
	Login        string
	PasswordHash string
	IsActive     bool
	IsAdmin      bool
}

// Channel — телеканал с HLS-потоком.
type Channel struct {
	ID     int64
	Number int
	Name   string
	// StreamPath — путь к плейлисту относительно базового адреса стрим-сервера.
	StreamPath string
	IsActive   bool
}

var (
	// ErrInvalidCredentials — неизвестный логин, неверный пароль или выключенный
	// пользователь. Причины намеренно не различаются, чтобы не раскрывать, какие логины существуют.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrNotFound — запрошенной записи нет (или она недоступна для показа).
	ErrNotFound = errors.New("not found")
	// ErrBusy — сервис временно перегружен проверкой паролей; запрос можно повторить позже.
	ErrBusy = errors.New("service is busy")
)
