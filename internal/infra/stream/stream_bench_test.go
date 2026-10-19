package stream

import (
	"testing"
	"time"
)

func BenchmarkSign(b *testing.B) {
	s, _ := New([]byte("bench-key"), func() time.Time { return t0 })
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Sign("http://stream.local/live/ch1/index.m3u8", 1, time.Minute); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyLink(b *testing.B) {
	s, _ := New([]byte("bench-key"), func() time.Time { return t0 })
	link, err := s.Sign("/live/ch1/index.m3u8", 1, time.Minute)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.VerifyLink(link); err != nil {
			b.Fatal(err)
		}
	}
}
