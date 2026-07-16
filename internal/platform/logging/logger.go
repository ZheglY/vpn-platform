package logging

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Environment string
	Level       string
}

func New(cfg Config) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if cfg.Level != "" {
		if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
			return nil, fmt.Errorf("parse log level: %w", err)
		}
	}

	var zapCfg zap.Config
	if cfg.Environment == "production" {
		zapCfg = zap.NewProductionConfig()
	} else {
		zapCfg = zap.NewDevelopmentConfig()
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)
	zapCfg.DisableStacktrace = true
	return zapCfg.Build()
}
