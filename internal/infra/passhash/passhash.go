// Package passhash хэширует и проверяет пароли алгоритмом argon2id.
//
// Хэш хранится в PHC-формате:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// Каждая операция занимает около 19 МиБ памяти и десятки миллисекунд CPU,
// поэтому число одновременных операций ограничено семафором. Рабочая память
// порядка concurrency * 19 МиБ (плюс накладные расходы runtime и ещё не собранный
// GC мусор), а перегрузка превращается в ErrBusy, а не в рост очереди и OOM.
//
// Ограничение: argon2.IDKey не умеет прерываться, поэтому отмена контекста
// действует только на ожидание слота. Уже начатое вычисление (десятки мс) доводится до конца.
package passhash

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Параметры по умолчанию соответствуют рекомендации OWASP (m=19 MiB, t=2, p=1).
const (
	memoryKiB = 19 * 1024
	timeCost  = 2
	threads   = 1
	saltLen   = 16
	keyLen    = 32

	// Верхние границы входных данных: защита от обработки гигантских строк.
	maxEncodedLen  = 256
	maxPasswordLen = 1024
)

var (
	// ErrBusy означает, что свободный слот проверки не появился вовремя.
	// Транспорт должен ответить 503 с Retry-After.
	ErrBusy = errors.New("passhash: too many concurrent hash operations")
	// ErrInvalidHash означает, что строка не является корректным argon2id PHC-хэшем.
	ErrInvalidHash = errors.New("passhash: invalid hash format")
	// ErrPasswordTooLong означает, что пароль длиннее maxPasswordLen байт.
	ErrPasswordTooLong = errors.New("passhash: password is too long")
)

// Hasher выполняет argon2id с ограничением параллелизма.
type Hasher struct {
	sem  chan struct{}
	wait time.Duration

	// derive подменяется в тестах, чтобы не тратить память и время.
	derive func(password, salt []byte, t, m uint32, p uint8, n uint32) []byte
}

// New создаёт Hasher. concurrency — число одновременных операций, wait — сколько
// ждать свободный слот. Значения меньше 1 приводятся к 1 и к нулевому ожиданию.
func New(concurrency int, wait time.Duration) *Hasher {
	if concurrency < 1 {
		concurrency = 1
	}
	if wait < 0 {
		wait = 0
	}
	return &Hasher{
		sem:    make(chan struct{}, concurrency),
		wait:   wait,
		derive: argon2.IDKey,
	}
}

// Hash возвращает PHC-строку для пароля со случайной солью.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if len(password) > maxPasswordLen {
		return "", ErrPasswordTooLong
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("passhash: read salt: %w", err)
	}
	key := h.derive([]byte(password), salt, timeCost, memoryKiB, threads, keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, timeCost, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify сообщает, подходит ли пароль к PHC-строке encoded.
// Неверный пароль не является ошибкой: возвращается (false, nil).
// Ошибки: ErrInvalidHash для некорректной строки, ErrBusy при перегрузке.
// Формат хэша проверяется до занятия слота, чтобы мусор не вытеснял настоящие проверки.
// Слишком длинный пароль заведомо не подходит (Hash такой не создаёт): (false, nil).
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	p, err := parse(encoded)
	if err != nil {
		return false, err
	}
	if len(password) > maxPasswordLen {
		return false, nil
	}

	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	got := h.derive([]byte(password), p.salt, p.t, p.m, p.p, uint32(len(p.key))) //nolint:gosec // длина ограничена в parse
	return subtle.ConstantTimeCompare(got, p.key) == 1, nil
}

// acquire занимает слот семафора. Ожидание ограничено ctx и таймаутом wait.
// Возвращаемая функция освобождает слот и должна вызываться ровно один раз.
func (h *Hasher) acquire(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBusy, err)
	}
	release = func() { <-h.sem }

	// Быстрый путь без таймера.
	select {
	case h.sem <- struct{}{}:
		return release, nil
	default:
	}
	if h.wait == 0 {
		return nil, ErrBusy
	}

	timer := time.NewTimer(h.wait)
	defer timer.Stop()

	select {
	case h.sem <- struct{}{}:
		// select выбирает случайный готовый case: слот мог достаться уже после отмены.
		if err := ctx.Err(); err != nil {
			release()
			return nil, fmt.Errorf("%w: %w", ErrBusy, err)
		}
		return release, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	case <-timer.C:
		return nil, ErrBusy
	}
}

type params struct {
	m    uint32
	t    uint32
	p    uint8
	salt []byte
	key  []byte
}

func parse(encoded string) (params, error) {
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, key]
	if len(encoded) > maxEncodedLen {
		return params{}, ErrInvalidHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params{}, ErrInvalidHash
	}

	// Sscanf не требует полного совпадения ("v=19abc" прошло бы), поэтому разбираем строго.
	versionStr, ok := strings.CutPrefix(parts[2], "v=")
	if !ok {
		return params{}, ErrInvalidHash
	}
	if version, err := strconv.ParseUint(versionStr, 10, 32); err != nil || version != argon2.Version {
		return params{}, ErrInvalidHash
	}

	var m, t, th uint64
	seen := map[string]bool{}
	for kv := range strings.SplitSeq(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || seen[k] {
			return params{}, ErrInvalidHash // нет "=" или параметр повторён
		}
		seen[k] = true

		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return params{}, ErrInvalidHash
		}
		switch k {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			th = n
		default:
			return params{}, ErrInvalidHash
		}
	}
	// Принимаются только параметры, с которыми Hash создаёт хэши. Так стоимость одной
	// проверки известна заранее, а память ограничена concurrency * 19 МиБ: подменённый
	// хэш с m=256 МиБ не обойдёт семафор. При повышении стоимости в будущем
	// сюда нужно добавить список допустимых наборов и перехэширование при входе.
	if len(seen) != 3 || m != memoryKiB || t != timeCost || th != threads {
		return params{}, ErrInvalidHash
	}
	p := params{m: memoryKiB, t: timeCost, p: threads}

	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(p.salt) != saltLen {
		return params{}, ErrInvalidHash
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(p.key) != keyLen {
		return params{}, ErrInvalidHash
	}
	return p, nil
}
