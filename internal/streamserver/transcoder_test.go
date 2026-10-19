package streamserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// fakeFFmpeg — скрипт вместо ffmpeg: пишет плейлист и сегмент в каталог из последнего
// аргумента и живёт, пока его не остановят. Каждый запуск дописывает строку в starts.log.
const fakeFFmpegScript = `#!/bin/sh
for last in "$@"; do :; done
echo started >> "$FAKE_STARTS"
if [ -f label.ass ]; then cp label.ass label.seen; fi
printf 'TSDATA' > seg00001.ts
printf '#EXTM3U\n#EXTINF:2.0,\nseg00001.ts\n' > "$last"
exec sleep 60
`

type fakeChannels struct {
	calls atomic.Int32
	byKey map[string]domain.Channel
}

func (f *fakeChannels) GetChannelByStreamPath(_ context.Context, p string) (domain.Channel, error) {
	f.calls.Add(1)
	c, ok := f.byKey[p]
	if !ok {
		return domain.Channel{}, domain.ErrNotFound
	}
	return c, nil
}

func newTestTranscoder(t *testing.T, font string, idle time.Duration) (*Transcoder, *fakeChannels, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(bin, []byte(fakeFFmpegScript), 0o700); err != nil { //nolint:gosec // тестовый скрипт должен быть исполняемым
		t.Fatal(err)
	}
	input := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(input, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(dir, "starts.log")
	t.Setenv("FAKE_STARTS", starts)

	ch := &fakeChannels{byKey: map[string]domain.Channel{
		"/live/channel1/index.m3u8": {ID: 1, Number: 1, Name: "Первый", IsActive: true},
	}}
	tc, err := NewTranscoder(slog.New(slog.DiscardHandler), ch, Options{
		FFmpeg: bin, Input: input, FontFamily: font, Idle: idle, StartTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	return tc, ch, starts
}

func readAll(t *testing.T, rc io.ReadCloser, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func startCount(t *testing.T, starts string) int {
	t.Helper()
	b, err := os.ReadFile(starts) //nolint:gosec // путь из t.TempDir
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "started")
}

func TestTranscoderServesAndReusesProcess(t *testing.T) {
	tc, ch, starts := newTestTranscoder(t, "font.ttf", time.Minute)
	ctx := context.Background()

	rc, err := tc.Open(ctx, "/live/channel1/index.m3u8")
	if got := readAll(t, rc, err); !strings.Contains(got, "seg00001.ts") {
		t.Fatalf("playlist = %q", got)
	}
	rc, err = tc.Open(ctx, "/live/channel1/seg00001.ts")
	if got := readAll(t, rc, err); got != "TSDATA" {
		t.Fatalf("segment = %q", got)
	}

	// Повторный запрос плейлиста не запускает второй ffmpeg и не ходит в БД.
	rc, err = tc.Open(ctx, "/live/channel1/index.m3u8")
	readAll(t, rc, err)
	if n := startCount(t, starts); n != 1 {
		t.Errorf("ffmpeg started %d times, want 1", n)
	}
	if n := ch.calls.Load(); n != 1 {
		t.Errorf("channel lookups = %d, want 1", n)
	}
}

func TestTranscoderWritesLabelFile(t *testing.T) {
	tc, _, _ := newTestTranscoder(t, "font.ttf", time.Minute)
	rc, err := tc.Open(context.Background(), "/live/channel1/index.m3u8")
	readAll(t, rc, err)

	tc.mu.Lock()
	s := tc.sessions["/live/channel1/index.m3u8"]
	tc.mu.Unlock()
	got, err := os.ReadFile(filepath.Join(s.dir, "label.seen")) //nolint:gosec // каталог создан Transcoder
	if err != nil || !strings.Contains(string(got), ",Label,,0,0,0,,Первый\n") || !strings.Contains(string(got), "Style: Label,font.ttf,") {
		t.Fatalf("label.ass = %q, %v", got, err)
	}
}

func TestTranscoderNoLabelWithoutFont(t *testing.T) {
	tc, _, _ := newTestTranscoder(t, "", time.Minute)
	rc, err := tc.Open(context.Background(), "/live/channel1/index.m3u8")
	readAll(t, rc, err)

	tc.mu.Lock()
	s := tc.sessions["/live/channel1/index.m3u8"]
	tc.mu.Unlock()
	if _, err := os.Stat(filepath.Join(s.dir, "label.ass")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("label.ass exists without font: %v", err)
	}
}

func TestTranscoderNotFound(t *testing.T) {
	tc, _, starts := newTestTranscoder(t, "", time.Minute)
	ctx := context.Background()
	for _, p := range []string{
		"/live/channel9/index.m3u8",       // нет такого канала
		"/live/channel1/seg00001.ts",      // сегмент до запуска потока
		"/live/channel1/../../etc/passwd", // не сегмент
		"/live/channel1/index.txt",
	} {
		if _, err := tc.Open(ctx, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) err = %v, want ErrNotFound", p, err)
		}
	}
	if n := startCount(t, starts); n != 0 {
		t.Errorf("ffmpeg started %d times for unknown paths", n)
	}
}

func TestTranscoderStopsIdleAndRestarts(t *testing.T) {
	tc, _, starts := newTestTranscoder(t, "", time.Second)
	ctx := context.Background()
	rc, err := tc.Open(ctx, "/live/channel1/index.m3u8")
	readAll(t, rc, err)

	tc.mu.Lock()
	dir := tc.sessions["/live/channel1/index.m3u8"].dir
	tc.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for {
		tc.mu.Lock()
		n := len(tc.sessions)
		tc.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle session was not stopped")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// reap удаляет сессию из карты до очистки каталога, поэтому ждём удаления.
	for {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session dir was not removed")
		}
		time.Sleep(50 * time.Millisecond)
	}

	rc, err = tc.Open(ctx, "/live/channel1/index.m3u8")
	readAll(t, rc, err)
	if n := startCount(t, starts); n != 2 {
		t.Errorf("ffmpeg started %d times, want 2 (restart after idle)", n)
	}
}

func TestTranscoderStartFailure(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(input, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ch := &fakeChannels{byKey: map[string]domain.Channel{"/p/index.m3u8": {Name: "A"}}}
	tc, err := NewTranscoder(slog.New(slog.DiscardHandler), ch, Options{
		FFmpeg: filepath.Join(dir, "no-such-ffmpeg"), Input: input, Idle: time.Minute, StartTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	if _, err := tc.Open(context.Background(), "/p/index.m3u8"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want start error", err)
	}
}

func TestNewTranscoderMissingInput(t *testing.T) {
	if _, err := NewTranscoder(slog.New(slog.DiscardHandler), &fakeChannels{}, Options{Input: "/no/such/file.mp4"}); err == nil {
		t.Fatal("expected error for missing input")
	}
}

func TestFFmpegArgs(t *testing.T) {
	with := strings.Join(ffmpegArgs(Options{Input: "/in.mp4", FontFamily: "F"}), " ")
	for _, want := range []string{"-stream_loop -1", "-re", "-i /in.mp4", "-vf ass=filename=label.ass", "-f hls", "index.m3u8"} {
		if !strings.Contains(with, want) {
			t.Errorf("args missing %q: %s", want, with)
		}
	}
	dir := strings.Join(ffmpegArgs(Options{Input: "/in.mp4", FontFamily: "F", FontsDir: "/fonts"}), " ")
	if !strings.Contains(dir, "ass=filename=label.ass:fontsdir='/fonts'") {
		t.Errorf("fontsdir missing: %s", dir)
	}
	without := strings.Join(ffmpegArgs(Options{Input: "/in.mp4"}), " ")
	if strings.Contains(without, "-vf") {
		t.Errorf("video filter present without font: %s", without)
	}
}

func TestLabelASSEscapesName(t *testing.T) {
	got := labelASS("A,B", "Мат{\\an1}ч\\N\nТВ")
	if !strings.Contains(got, "Style: Label,A B,") {
		t.Errorf("font field not sanitized: %q", got)
	}
	// Теги оформления, обратные слэши и переводы строк из названия обезврежены.
	if !strings.HasSuffix(got, ",Label,,0,0,0,,Мат(/an1)ч/N ТВ\n") {
		t.Errorf("text not sanitized: %q", got)
	}
}
