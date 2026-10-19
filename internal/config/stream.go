package config

import "time"

// StreamConfig содержит настройки учебного стрим-сервера (cmd/stream).
type StreamConfig struct {
	Common
	HTTPTimeouts
	// DB — Postgres, из которого берутся названия каналов (только чтение).
	DB Database

	HTTPAddr string
	// PprofAddr — адрес pprof-сервера; пусто — выключен.
	PprofAddr string

	// FFmpegBin — исполняемый файл ffmpeg.
	FFmpegBin string
	// FontFamily — семейство шрифта для названия канала (с кириллицей).
	FontFamily string
	// FontsDir — каталог со шрифтами для libass; пусто — системные шрифты.
	FontsDir string
	// TranscodeIdle — через сколько без зрителей останавливать ffmpeg канала.
	TranscodeIdle time.Duration
	// TranscodeStartTimeout — сколько ждать первый сегмент после запуска ffmpeg.
	TranscodeStartTimeout time.Duration
}

// LoadStream читает настройки стрим-сервера. Правила разбора те же, что у Load.
func LoadStream(getenv func(string) string) (StreamConfig, error) {
	return load(getenv, func(l *loader) StreamConfig {
		return StreamConfig{
			Common:       l.common(),
			HTTPTimeouts: l.httpTimeouts(),
			DB:           l.database(4),

			HTTPAddr:  l.str("STREAM_HTTP_ADDR", ":8081"),
			PprofAddr: l.str("STREAM_PPROF_ADDR", ""),

			FFmpegBin:             l.str("FFMPEG_BIN", "ffmpeg"),
			FontFamily:            l.str("STREAM_FONT_FAMILY", "DejaVu Sans"),
			FontsDir:              l.str("STREAM_FONTS_DIR", ""),
			TranscodeIdle:         l.duration("STREAM_IDLE_TIMEOUT", 30*time.Second),
			TranscodeStartTimeout: l.duration("STREAM_START_TIMEOUT", 15*time.Second),
		}
	})
}
