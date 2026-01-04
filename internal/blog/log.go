package blog

import (
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type StdLogAdapter struct {
	Logger zerolog.Logger
}

func (w *StdLogAdapter) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))

	if strings.Contains(msg, "error") || strings.Contains(msg, "Error") {
		w.Logger.Error().Msg(msg)
	} else {
		w.Logger.Info().Msg(msg)
	}

	return len(p), nil
}

var (
	managerLogger zerolog.Logger
	serverLogger  zerolog.Logger
	blogLogger    zerolog.Logger
	telemLogger   zerolog.Logger
	mdLogger      zerolog.Logger
	configLogger  zerolog.Logger
)

func InitLoggers() error {
	managerLogger = log.Logger.With().Str("subsystem", "manager").Logger()
	serverLogger = log.Logger.With().Str("subsystem", "server").Logger()
	blogLogger = log.Logger.With().Str("subsystem", "blog").Logger()
	telemLogger = log.Logger.With().Str("subsystem", "telemetry").Logger()
	mdLogger = log.Logger.With().Str("subsystem", "md").Logger()
	configLogger = log.Logger.With().Str("subsystem", "config").Logger()
	return nil
}
