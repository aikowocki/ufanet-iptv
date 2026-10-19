package passhash

import (
	"context"
	"testing"
	"time"
)

// BenchmarkVerify измеряет реальный argon2id (OWASP-параметры) без подмены derive.
// Именно эта цена платится на каждый запрос с Basic Auth, пока нет кэша проверок.
func BenchmarkVerify(b *testing.B) {
	ctx := context.Background()
	h := New(1, time.Minute)
	enc, err := h.Hash(ctx, "alice123")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if ok, err := h.Verify(ctx, "alice123", enc); err != nil || !ok {
			b.Fatalf("verify: ok=%v err=%v", ok, err)
		}
	}
}

// BenchmarkVerifyParallel показывает пропускную способность при конкуренции за слоты.
func BenchmarkVerifyParallel(b *testing.B) {
	ctx := context.Background()
	h := New(4, time.Minute)
	enc, err := h.Hash(ctx, "alice123")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ok, err := h.Verify(ctx, "alice123", enc); err != nil || !ok {
				b.Errorf("verify: ok=%v err=%v", ok, err)
				return
			}
		}
	})
}
