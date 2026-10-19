package streamserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/infra/stream"
)

// benchPlaylist повторяет окно HLS из 6 сегментов, которое пишет ffmpeg.
const benchPlaylist = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:100
#EXTINF:2.000000,
seg100.ts
#EXTINF:2.000000,
seg101.ts
#EXTINF:2.000000,
seg102.ts
#EXTINF:2.000000,
seg103.ts
#EXTINF:2.000000,
seg104.ts
#EXTINF:2.000000,
seg105.ts
`

type memSource struct{ playlist string }

func (s memSource) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.playlist)), nil
}

// BenchmarkPlaylist: проверка подписи ссылки и подпись шести сегментов на каждый запрос плейлиста.
// Плеер опрашивает плейлист каждые ~2 с, поэтому это горячий путь стрим-сервера.
func BenchmarkPlaylist(b *testing.B) {
	signer, err := stream.New([]byte("bench-key"), nil)
	if err != nil {
		b.Fatal(err)
	}
	link, err := signer.Sign("/live/ch1/index.m3u8", 1, time.Hour)
	if err != nil {
		b.Fatal(err)
	}
	h := NewHandler(slog.New(slog.DiscardHandler), signer, memSource{benchPlaylist})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, link, nil))
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}
