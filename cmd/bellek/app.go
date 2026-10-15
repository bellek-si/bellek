package main

import (
	"fmt"

	"github.com/bellek-si/bellek/client"
	"github.com/bellek-si/bellek/internal/config"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type app struct {
	cfg *config.Config
	log *zap.SugaredLogger
}

func newAppWith(newLogger func(level string) (*zap.SugaredLogger, error)) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	log, err := newLogger(cfg.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	return &app{cfg: cfg, log: log}, nil
}

func stderrLogger(level string) (*zap.SugaredLogger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}
	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Encoding:         "console",
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			MessageKey:     "message",
			LevelKey:       "level",
			TimeKey:        "time",
			CallerKey:      "caller",
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
	}
	l, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return l.Sugar(), nil
}

func (a *app) close() {
	_ = a.log.Sync()
}

func (a *app) client(opts ...client.Option) *client.Client {
	url, token := a.remote()
	return client.New(url, token, opts...)
}

func (a *app) remote() (string, string) {
	if a.cfg.ClearText() {
		a.log.Warnw("BELLEK_URL is plain http to another machine, so the key and every capture cross the network unencrypted", "url", a.cfg.ServerURL)
	}
	return a.cfg.ServerURL, a.cfg.Token
}

func withApp(fn func(cmd *cobra.Command, a *app) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a, err := newAppWith(stderrLogger)
		if err != nil {
			return err
		}
		defer a.close()
		return fn(cmd, a)
	}
}

func withClient(fn func(cmd *cobra.Command, a *app, c *client.Client) error) func(*cobra.Command, []string) error {
	return withApp(func(cmd *cobra.Command, a *app) error {
		return fn(cmd, a, a.client())
	})
}
