package streamserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// Ошибки источника потока.
var (
	// ErrNotFound — такого канала или сегмента нет.
	ErrNotFound = errors.New("stream not found")
	// ErrUnavailable — поток не удалось запустить вовремя; запрос можно повторить.
	ErrUnavailable = errors.New("stream unavailable")
)

// Source отдаёт содержимое плейлиста или сегмента по пути URL (например, /live/channel1/index.m3u8).
type Source interface {
	Open(ctx context.Context, urlPath string) (io.ReadCloser, error)
}

// ChannelFinder находит включённый канал по stream_path.
type ChannelFinder interface {
	GetChannelByStreamPath(ctx context.Context, streamPath string) (domain.Channel, error)
}

// Options настраивает Transcoder.
type Options struct {
	// FFmpeg — исполняемый файл ffmpeg.
	FFmpeg string
	// FontFamily — семейство шрифта для названия канала (должно содержать кириллицу).
	// Пусто — канал транслируется без подписи.
	FontFamily string
	// FontsDir — каталог с шрифтами для libass. Пусто — системные шрифты (fontconfig).
	FontsDir string
	// Idle — через сколько без запросов ffmpeg канала останавливается.
	Idle time.Duration
	// StartTimeout — сколько ждать первый сегмент после запуска ffmpeg.
	StartTimeout time.Duration
}

// segmentName — единственный допустимый вид имени сегмента; защищает от обхода каталогов.
var segmentName = regexp.MustCompile(`^seg\d{5}\.ts$`)

// Transcoder запускает по одному ffmpeg на канал и только пока на канал смотрят.
// ffmpeg читает входной файл по кругу, рисует в углу название канала и пишет HLS во временный каталог.
type Transcoder struct {
	log      *slog.Logger
	channels ChannelFinder
	opt      Options
	root     string

	ctx    context.Context //nolint:containedctx // жизненный цикл фоновых процессов привязан к Transcoder
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	sessions map[string]*session // ключ: путь плейлиста
}

type session struct {
	dir     string
	cancel  context.CancelFunc
	done    chan struct{} // закрывается, когда ffmpeg завершился
	lastUse atomic.Int64  // unix nano последнего запроса
}

func (s *session) touch() { s.lastUse.Store(time.Now().UnixNano()) }

func (s *session) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// NewTranscoder создаёт Transcoder и запускает фоновую остановку неиспользуемых потоков.
// Вызывающий обязан вызвать Close.
func NewTranscoder(log *slog.Logger, channels ChannelFinder, opt Options) (*Transcoder, error) {
	if opt.FFmpeg == "" {
		opt.FFmpeg = "ffmpeg"
	}
	root, err := os.MkdirTemp("", "iptv-stream-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Transcoder{
		log: log, channels: channels, opt: opt, root: root,
		ctx: ctx, cancel: cancel, sessions: map[string]*session{},
	}
	t.wg.Add(1)
	go t.reap()
	return t, nil
}

// Close останавливает все ffmpeg и удаляет временные файлы.
func (t *Transcoder) Close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.cancel() // exec.CommandContext останавливает все процессы
	t.wg.Wait()
	_ = os.RemoveAll(t.root)
}

// Open возвращает плейлист или сегмент. Плейлист запускает ffmpeg канала, если он ещё не работает.
func (t *Transcoder) Open(ctx context.Context, urlPath string) (io.ReadCloser, error) {
	switch path.Ext(urlPath) {
	case ".m3u8":
		return t.openPlaylist(ctx, urlPath)
	case ".ts":
		return t.openSegment(urlPath)
	}
	return nil, ErrNotFound
}

func (t *Transcoder) openPlaylist(ctx context.Context, urlPath string) (io.ReadCloser, error) {
	s, err := t.session(ctx, urlPath)
	if err != nil {
		return nil, err
	}
	return t.waitFile(ctx, s, "index.m3u8")
}

func (t *Transcoder) openSegment(urlPath string) (io.ReadCloser, error) {
	name := path.Base(urlPath)
	if !segmentName.MatchString(name) {
		return nil, ErrNotFound
	}
	t.mu.Lock()
	s := t.sessions[path.Dir(urlPath)+"/index.m3u8"]
	t.mu.Unlock()
	if s == nil || !s.alive() {
		return nil, ErrNotFound
	}
	s.touch()
	f, err := os.Open(filepath.Join(s.dir, name)) //nolint:gosec // name прошёл проверку segmentName
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound // сегмент уже удалён ffmpeg
	}
	if err != nil {
		return nil, fmt.Errorf("open segment: %w", err)
	}
	return f, nil
}

// waitFile ждёт, пока ffmpeg создаст файл (первый сегмент и плейлист), и открывает его.
func (t *Transcoder) waitFile(ctx context.Context, s *session, name string) (io.ReadCloser, error) {
	deadline := time.NewTimer(t.opt.StartTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		f, err := os.Open(filepath.Join(s.dir, name)) //nolint:gosec // name — константа вызывающего кода
		if err == nil {
			s.touch()
			return f, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
			return nil, ErrUnavailable
		case <-deadline.C:
			return nil, ErrUnavailable
		case <-tick.C:
		}
	}
}

// session возвращает работающую сессию канала или запускает новую.
func (t *Transcoder) session(ctx context.Context, playlistPath string) (*session, error) {
	if s := t.running(playlistPath); s != nil {
		return s, nil
	}
	// Запрос в БД делаем вне блокировки и только когда ffmpeg ещё не запущен.
	ch, err := t.channels.GetChannelByStreamPath(ctx, playlistPath)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find channel: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, ErrUnavailable
	}
	if s := t.sessions[playlistPath]; s != nil {
		if s.alive() {
			s.touch()
			return s, nil
		}
		t.discard(s) // ffmpeg упал: уберём остатки и запустим заново
	}
	s, err := t.start(ch)
	if err != nil {
		return nil, err
	}
	t.sessions[playlistPath] = s
	return s, nil
}

func (t *Transcoder) running(playlistPath string) *session {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.sessions[playlistPath]; s != nil && s.alive() {
		s.touch()
		return s
	}
	return nil
}

// start запускает ffmpeg для канала. Вызывается под t.mu.
func (t *Transcoder) start(ch domain.Channel) (*session, error) {
	dir, err := os.MkdirTemp(t.root, "ch")
	if err != nil {
		return nil, fmt.Errorf("channel dir: %w", err)
	}
	// Название идёт через файл субтитров, а не через аргумент фильтра: текст из БД
	// не может изменить сам фильтр.
	if t.opt.FontFamily != "" {
		if err := os.WriteFile(filepath.Join(dir, "label.ass"), []byte(labelASS(t.opt.FontFamily, ch.Name)), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("write label: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(t.ctx)
	cmd := exec.CommandContext(ctx, t.opt.FFmpeg, ffmpegArgs(t.opt, ch.ID)...) //nolint:gosec // бинарник и аргументы заданы конфигурацией
	cmd.Dir = dir
	cmd.Stderr = &logWriter{log: t.log.With("channel", ch.Name), level: slog.LevelWarn}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	s := &session{dir: dir, cancel: cancel, done: make(chan struct{})}
	s.touch()
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		err := cmd.Wait()
		close(s.done)
		if ctx.Err() == nil { // не мы остановили, значит ffmpeg упал сам
			t.log.Error("ffmpeg exited", "channel", ch.Name, "err", err)
		}
	}()
	t.log.Info("stream started", "channel", ch.Name, "number", ch.Number)
	return s, nil
}

// discard останавливает сессию и удаляет её файлы. Не держит t.mu дольше, чем нужно для Wait.
func (t *Transcoder) discard(s *session) {
	s.cancel()
	<-s.done
	_ = os.RemoveAll(s.dir) //nolint:gosec // каталог создан самим Transcoder
}

// reap останавливает потоки, на которые давно никто не смотрит.
func (t *Transcoder) reap() {
	defer t.wg.Done()
	period := max(t.opt.Idle/3, time.Second)
	tick := time.NewTicker(period)
	defer tick.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-tick.C:
		}
		var stale []*session
		t.mu.Lock()
		for key, s := range t.sessions {
			idle := time.Since(time.Unix(0, s.lastUse.Load())) > t.opt.Idle
			if idle || !s.alive() {
				delete(t.sessions, key)
				stale = append(stale, s)
			}
		}
		t.mu.Unlock()
		for _, s := range stale {
			t.discard(s)
			t.log.Info("stream stopped", "reason", "idle")
		}
	}
}

// generatedVideo — набор синтетических видеорядов (фильтры-источники lavfi), все 1280x720, 25 к/с.
// Канал берёт ряд по своему id, поэтому соседние каналы выглядят по-разному, а один и тот же
// канал после перезапуска ffmpeg выглядит так же. Тяжёлые генераторы считаются в малом
// размере и растягиваются.
var generatedVideo = []string{
	"gradients=s=1280x720:r=25:speed=0.02:seed=%d",
	"life=s=320x180:r=25:ratio=0.15:mold=10:random_seed=%d:life_color=#00e0a0:death_color=#101830,format=rgb24,scale=1280:720:flags=neighbor",
	"cellauto=s=320x180:r=25:rule=110:random_seed=%d:random_fill_ratio=0.4,format=rgb24,scale=1280:720:flags=neighbor",
	"mandelbrot=s=640x360:r=25,scale=1280:720",
	"testsrc2=s=1280x720:r=25",
}

// generatedInput возвращает аргументы входа для синтетического источника канала:
// видеоряд из generatedVideo и тихий тон, частота которого зависит от канала.
func generatedInput(channelID int64) []string {
	n := int64(len(generatedVideo))
	idx := ((channelID % n) + n) % n
	video := generatedVideo[idx]
	if strings.Contains(video, "%d") {
		video = fmt.Sprintf(video, channelID)
	}
	freq := 220 + 110*((channelID%5+5)%5) // 220..660 Гц
	audio := fmt.Sprintf("sine=frequency=%d:sample_rate=44100,volume=0.1", freq)
	return []string{"-re", "-f", "lavfi", "-i", video, "-f", "lavfi", "-i", audio}
}

// ffmpegArgs собирает аргументы ffmpeg. Пути относительные: ffmpeg запускается в каталоге канала.
// channelID выбирает видеоряд и тон канала.
func ffmpegArgs(opt Options, channelID int64) []string {
	args := append([]string{"-hide_banner", "-loglevel", "warning"}, generatedInput(channelID)...)
	if opt.FontFamily != "" {
		vf := "ass=filename=label.ass"
		if opt.FontsDir != "" {
			vf += ":fontsdir='" + strings.ReplaceAll(opt.FontsDir, `'`, ``) + "'"
		}
		args = append(args, "-vf", vf)
	}
	return append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", "60", "-sc_threshold", "0",
		"-c:a", "aac", "-b:a", "128k", "-ar", "44100", "-ac", "2",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "6",
		"-hls_flags", "delete_segments+omit_endlist+independent_segments+temp_file",
		"-hls_segment_filename", "seg%05d.ts",
		"index.m3u8",
	)
}

// logWriter пишет вывод ffmpeg в slog.
type logWriter struct {
	log   *slog.Logger
	level slog.Level
}

func (w *logWriter) Write(p []byte) (int, error) {
	if msg := strings.TrimSpace(string(p)); msg != "" {
		w.log.Log(context.Background(), w.level, "ffmpeg", "output", msg)
	}
	return len(p), nil
}

// DefaultFontFamily — шрифт для названия канала; в docker-образе ставится пакетом font-dejavu.
const DefaultFontFamily = "DejaVu Sans"

// LabelSupported проверяет, что ffmpeg умеет рисовать название канала (фильтр ass на libass).
// Ошибка объясняет, почему названия не будет: потоки при этом работают, но без подписи.
//
// Фильтр drawtext не используется намеренно: в ffmpeg 6.1 из Alpine он обрезает кириллицу
// примерно вдвое («Первый» превращается в «Пер»), а libass корректно работает с UTF-8.
func LabelSupported(ctx context.Context, ffmpegBin string) error {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	out, err := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-filters").Output() //nolint:gosec // бинарник задан конфигурацией
	if err != nil {
		return fmt.Errorf("run %s -filters: %w", ffmpegBin, err)
	}
	if !strings.Contains(string(out), " ass ") {
		return errors.New("ffmpeg собран без фильтра ass (нет libass)")
	}
	return nil
}

// labelASS возвращает ASS-субтитры с названием канала в правом верхнем углу на полупрозрачной плашке.
// Размер сетки 1280x720 соответствует исходному ролику, libass масштабирует под реальный кадр.
func labelASS(fontFamily, name string) string {
	return "[Script Info]\n" +
		"ScriptType: v4.00+\n" +
		"PlayResX: 1280\n" +
		"PlayResY: 720\n" +
		"WrapStyle: 2\n\n" +
		"[V4+ Styles]\n" +
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, " +
		"Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, " +
		"Alignment, MarginL, MarginR, MarginV, Encoding\n" +
		// BorderStyle=3 рисует плашку цветом OutlineColour; Alignment=9 — правый верхний угол.
		"Style: Label," + assField(fontFamily) + ",44,&H00FFFFFF,&H00FFFFFF,&H8C000000,&H8C000000," +
		"0,0,0,0,100,100,0,0,3,10,0,9,32,32,32,1\n\n" +
		"[Events]\n" +
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:00.00,99:59:59.00,Label,,0,0,0,," + assText(name) + "\n"
}

// assField убирает запятые и переводы строк из значения поля стиля.
func assField(s string) string {
	return strings.NewReplacer(",", " ", "\n", " ", "\r", " ").Replace(s)
}

// assText делает название безопасным для ASS: фигурные скобки начинают теги оформления,
// обратный слэш начинает управляющие последовательности, а перевод строки ломает событие.
func assText(s string) string {
	return strings.NewReplacer("{", "(", "}", ")", "\\", "/", "\n", " ", "\r", " ").Replace(s)
}
