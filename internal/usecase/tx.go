// Package usecase содержит порты, общие для сценариев использования.
// Сами сценарии лежат в подпакетах (auth, catalog, playback).
package usecase

import "context"

// TxManager выполняет fn как одну атомарную единицу работы в хранилище.
type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
