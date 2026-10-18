package passhash

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	h := New(1, time.Second)
	ctx := context.Background()

	encoded, err := h.Hash(ctx, "secret")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected PHC prefix: %q", encoded)
	}

	ok, err := h.Verify(ctx, "secret", encoded)
	if err != nil || !ok {
		t.Errorf("Verify(correct) = %v, %v; want true, nil", ok, err)
	}

	ok, err = h.Verify(ctx, "wrong", encoded)
	if err != nil || ok {
		t.Errorf("Verify(wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestHashUsesRandomSalt(t *testing.T) {
	h := New(1, time.Second)
	a, _ := h.Hash(context.Background(), "same")
	b, _ := h.Hash(context.Background(), "same")
	if a == b {
		t.Error("two hashes of the same password are identical")
	}
}

const (
	testSalt = "AAAAAAAAAAAAAAAAAAAAAA"                      // 16 байт
	testKey  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 32 байта
	// testHash — синтаксически корректный хэш с допустимыми параметрами.
	testHash = "$argon2id$v=19$m=19456,t=2,p=1$" + testSalt + "$" + testKey
)

func TestVerifyInvalidHash(t *testing.T) {
	h := New(1, time.Second)
	tests := map[string]string{
		"empty":            "",
		"garbage":          "not-a-hash",
		"too long":         testHash + strings.Repeat("A", 300),
		"wrong algo":       "$argon2i$v=19$m=19456,t=2,p=1$" + testSalt + "$" + testKey,
		"wrong version":    "$argon2id$v=16$m=19456,t=2,p=1$" + testSalt + "$" + testKey,
		"version suffix":   "$argon2id$v=19abc$m=19456,t=2,p=1$" + testSalt + "$" + testKey,
		"missing parts":    "$argon2id$v=19$m=19456,t=2,p=1$" + testSalt,
		"bad param value":  "$argon2id$v=19$m=x,t=2,p=1$" + testSalt + "$" + testKey,
		"unknown param":    "$argon2id$v=19$m=19456,t=2,p=1,x=1$" + testSalt + "$" + testKey,
		"duplicate param":  "$argon2id$v=19$m=19456,m=1024,t=2,p=1$" + testSalt + "$" + testKey,
		"missing param":    "$argon2id$v=19$m=19456,t=2$" + testSalt + "$" + testKey,
		"huge memory":      "$argon2id$v=19$m=4194304,t=2,p=1$" + testSalt + "$" + testKey,
		"weak memory":      "$argon2id$v=19$m=1024,t=2,p=1$" + testSalt + "$" + testKey,
		"other time cost":  "$argon2id$v=19$m=19456,t=3,p=1$" + testSalt + "$" + testKey,
		"zero time":        "$argon2id$v=19$m=19456,t=0,p=1$" + testSalt + "$" + testKey,
		"threads too many": "$argon2id$v=19$m=19456,t=2,p=300$" + testSalt + "$" + testKey,
		"bad salt":         "$argon2id$v=19$m=19456,t=2,p=1$!!!$" + testKey,
		"short salt":       "$argon2id$v=19$m=19456,t=2,p=1$AAAA$" + testKey,
		"bad key":          "$argon2id$v=19$m=19456,t=2,p=1$" + testSalt + "$!!!",
		"short key":        "$argon2id$v=19$m=19456,t=2,p=1$" + testSalt + "$AAAA",
	}
	for name, enc := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := h.Verify(context.Background(), "x", enc)
			if !errors.Is(err, ErrInvalidHash) || ok {
				t.Errorf("Verify = %v, %v; want false, ErrInvalidHash", ok, err)
			}
		})
	}
}

func TestPasswordTooLong(t *testing.T) {
	h := fakeHasher(1, time.Second, nil)
	long := strings.Repeat("a", maxPasswordLen+1)

	if _, err := h.Hash(context.Background(), long); !errors.Is(err, ErrPasswordTooLong) {
		t.Errorf("Hash err = %v, want ErrPasswordTooLong", err)
	}
	if ok, err := h.Verify(context.Background(), long, testHash); ok || err != nil {
		t.Errorf("Verify = %v, %v; want false, nil", ok, err)
	}
	if _, err := h.Hash(context.Background(), strings.Repeat("a", maxPasswordLen)); err != nil {
		t.Errorf("Hash at limit: %v", err)
	}
}

func TestZeroWaitReturnsBusyImmediately(t *testing.T) {
	h := New(1, 0)
	release := occupy(t, h)
	defer release()

	start := time.Now()
	if _, err := h.Hash(context.Background(), "p"); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
	if time.Since(start) > time.Second {
		t.Error("zero wait should not block")
	}
}

// Парсер не должен паниковать и принимать мусор на произвольном вводе.
func FuzzParse(f *testing.F) {
	f.Add(testHash)
	f.Add("")
	f.Add("$argon2id$v=19$m=19456,t=2,p=1$$")
	f.Add("$$$$$$")
	f.Fuzz(func(t *testing.T, s string) {
		p, err := parse(s)
		if err != nil {
			if !errors.Is(err, ErrInvalidHash) {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		if p.m != memoryKiB || p.t != timeCost || p.p != threads || len(p.salt) != saltLen || len(p.key) != keyLen {
			t.Fatalf("accepted unexpected params: %+v", p)
		}
	})
}

// fakeHasher возвращает Hasher с быстрой подменой KDF. Функция entered вызывается
// на входе в KDF и может блокироваться, удерживая слот.
func fakeHasher(concurrency int, wait time.Duration, entered func()) *Hasher {
	h := New(concurrency, wait)
	h.derive = func(password, _ []byte, _, _ uint32, _ uint8, n uint32) []byte {
		if entered != nil {
			entered()
		}
		out := make([]byte, n)
		copy(out, password)
		return out
	}
	return h
}

func TestConcurrencyLimit(t *testing.T) {
	const limit = 3
	var cur, peak atomic.Int32

	h := fakeHasher(limit, 5*time.Second, func() {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		cur.Add(-1)
	})

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := h.Hash(context.Background(), "p"); err != nil {
				t.Errorf("Hash: %v", err)
			}
		})
	}
	wg.Wait()

	if got := peak.Load(); got > limit {
		t.Errorf("peak concurrency = %d, want <= %d", got, limit)
	}
	if got := peak.Load(); got < 2 {
		t.Errorf("peak concurrency = %d, expected parallel execution", got)
	}
}

// occupy занимает единственный слот и возвращает функцию для его освобождения.
func occupy(t *testing.T, h *Hasher) (release func()) {
	t.Helper()
	started := make(chan struct{})
	block := make(chan struct{})
	done := make(chan struct{})

	var once sync.Once
	h.derive = func(_, _ []byte, _, _ uint32, _ uint8, n uint32) []byte {
		once.Do(func() { close(started); <-block })
		return make([]byte, n)
	}
	go func() {
		defer close(done)
		_, _ = h.Hash(context.Background(), "p")
	}()
	<-started
	return func() { close(block); <-done }
}

func TestBusyOnTimeout(t *testing.T) {
	h := New(1, 20*time.Millisecond)
	release := occupy(t, h)
	defer release()

	_, err := h.Hash(context.Background(), "p")
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Hash err = %v, want ErrBusy", err)
	}

	if _, err := h.Verify(context.Background(), "p", testHash); !errors.Is(err, ErrBusy) {
		t.Errorf("Verify err = %v, want ErrBusy", err)
	}
}

func TestBusyOnContextCancel(t *testing.T) {
	h := New(1, 10*time.Second)
	release := occupy(t, h)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := h.Hash(ctx, "p")
	if !errors.Is(err, ErrBusy) || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want ErrBusy wrapping context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("cancel did not interrupt waiting")
	}
}

func TestCancelledContextFailsImmediately(t *testing.T) {
	h := fakeHasher(1, time.Second, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Hash(ctx, "p"); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
}

func TestSlotReleasedAfterFailure(t *testing.T) {
	h := fakeHasher(1, 20*time.Millisecond, nil)

	// Verify с мусором не должен занимать слот, а успешные вызовы освобождают его.
	for range 5 {
		if _, err := h.Verify(context.Background(), "p", "garbage"); !errors.Is(err, ErrInvalidHash) {
			t.Fatalf("Verify err = %v, want ErrInvalidHash", err)
		}
		if _, err := h.Hash(context.Background(), "p"); err != nil {
			t.Fatalf("Hash after failure: %v", err)
		}
	}

	// Слот, который не удалось занять по тайм-ауту, тоже не утекает.
	release := occupy(t, h)
	if _, err := h.Hash(context.Background(), "p"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	release()
	if _, err := h.Hash(context.Background(), "p"); err != nil {
		t.Errorf("Hash after release: %v", err)
	}
}
