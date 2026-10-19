// Package middleware содержит HTTP middleware сервиса, по одному файлу на каждый.
package middleware

import "slices"

import "net/http"

// Middleware оборачивает обработчик.
type Middleware func(http.Handler) http.Handler

// Chain оборачивает h middleware так, что первый в списке становится внешним.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, mw := range slices.Backward(mws) {
		h = mw(h)
	}
	return h
}
