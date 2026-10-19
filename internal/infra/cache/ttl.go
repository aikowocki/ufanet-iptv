// Package cache содержит небольшой потокобезопасный кэш в памяти с TTL.
package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	val     V
	expires time.Time
}

// TTL хранит значения до истечения срока. Просроченные записи не возвращаются
// и удаляются при обращении; кроме того, Set не чаще раза в sweepEvery
// вычищает все просроченные, поэтому память не растёт от ключей, к которым не возвращаются.
type TTL[K comparable, V any] struct {
	mu         sync.Mutex
	m          map[K]entry[V]
	now        func() time.Time
	sweepEvery time.Duration
	lastSweep  time.Time
}

// NewTTL создаёт кэш. sweepEvery задаёт период очистки просроченных записей;
// now == nil означает time.Now.
func NewTTL[K comparable, V any](sweepEvery time.Duration, now func() time.Time) *TTL[K, V] {
	if now == nil {
		now = time.Now
	}
	return &TTL[K, V]{m: make(map[K]entry[V]), now: now, sweepEvery: sweepEvery, lastSweep: now()}
}

// Get возвращает значение, если оно есть и не просрочено.
func (c *TTL[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if ok && !c.now().Before(e.expires) {
		delete(c.m, key)
		ok = false
	}
	if !ok {
		var zero V
		return zero, false
	}
	return e.val, true
}

// Set сохраняет значение на ttl. Неположительный ttl ничего не сохраняет.
func (c *TTL[K, V]) Set(key K, val V, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Sub(c.lastSweep) >= c.sweepEvery {
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
			}
		}
		c.lastSweep = now
	}
	c.m[key] = entry[V]{val: val, expires: now.Add(ttl)}
}

// DeleteFunc удаляет записи, для которых pred вернул true. Перебирает весь кэш,
// поэтому подходит для редких операций (например, изменение пользователя админом).
func (c *TTL[K, V]) DeleteFunc(pred func(K, V) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.m {
		if pred(k, e.val) {
			delete(c.m, k)
		}
	}
}

// Clear удаляет все записи.
func (c *TTL[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.m)
}

// Len возвращает число записей, включая ещё не вычищенные просроченные.
func (c *TTL[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
