package main

import (
	"fmt"
	"io"
	"jakeblog/internal/blog"
	"log"
	"os"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
)

const (
	INFO = iota
	ERROR
	FATAL
)

func initLogMSG(level int, msg string) {
	env := blog.CheckAnonEnvironmental("BLOG_ENVMNT")
	switch level {
	case INFO:
		zlog.Info().Str("subsystem", "jakeserver").Str("env", env).Msg(msg)
	case ERROR:
		zlog.Error().Str("subsystem", "jakeserver").Str("env", env).Msg(msg)
	case FATAL:
		zlog.Fatal().Str("subsystem", "jakeserver").Str("env", env).Msg(msg)
	}
}

func initZLOG(level int) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	switch level {
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	env := blog.CheckAnonEnvironmental("BLOG_ENVMNT")
	lw, err := makeLogWriters()
	if err != nil {
		initLogMSG(FATAL, fmt.Sprintf("failed to make logWriters: %v", err))
	}
	zlog.Logger = zerolog.New(lw).With().
		Timestamp().
		Str("env", env).
		Logger()

	log.SetOutput(&blog.StdLogAdapter{Logger: zlog.Logger.With().Str("subsystem", "stdlib").Logger()})
	log.SetFlags(0)

	err = blog.InitLoggers()
	if err != nil {
		initLogMSG(FATAL, fmt.Sprintf("failed to init logging: %v", err))
	}
}

func makeLogWriters() (io.Writer, error) {
	prettyLog := blog.CheckAnonEnvironmentalFlag("PRETTY_LOGGING")
	fileLog := blog.CheckAnonEnvironmentalFlag("LOG_2_FILE")
	logPath := blog.CheckAnonEnvironmental("LOG_FILE")

	logWriters := make([]io.Writer, 0)

	if prettyLog && fileLog {
		logWriters = append(logWriters, zerolog.ConsoleWriter{Out: os.Stdout})

		lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %v", err)
		}
		logWriters = append(logWriters, lf)
	} else if prettyLog {
		logWriters = append(logWriters, zerolog.ConsoleWriter{Out: os.Stdout})
	} else if fileLog {
		lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %v", err)
		}
		logWriters = append(logWriters, lf)
	} else {
		logWriters = append(logWriters, os.Stdout)
	}

	var writer io.Writer
	if len(logWriters) == 1 {
		writer = logWriters[0]
	} else {
		writer = io.MultiWriter(logWriters...)
	}
	return writer, nil
}
