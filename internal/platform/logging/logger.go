package logging

import (
	"context"
	"fmt"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const otelContextField = "__otel_context"

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
	logger, err := zapCfg.Build()
	if err != nil {
		return nil, err
	}
	return logger.WithOptions(zap.WrapCore(func(stdout zapcore.Core) zapcore.Core {
		otelCore := otelzap.NewCore("github.com/ZheglY/vpn-platform")
		return zapcore.NewTee(
			newFilteredCore(stdout, stdoutFieldAllowed, false),
			newFilteredCore(otelCore, otelFieldAllowed, true),
		)
	})), nil
}

func Context(ctx context.Context) zap.Field {
	return zap.Reflect(otelContextField, ctx)
}

type filteredCore struct {
	core        zapcore.Core
	allow       func(zapcore.Field) bool
	clearCaller bool
}

func newFilteredCore(core zapcore.Core, allow func(zapcore.Field) bool, clearCaller bool) zapcore.Core {
	return &filteredCore{core: core, allow: allow, clearCaller: clearCaller}
}

func (c *filteredCore) Enabled(level zapcore.Level) bool {
	return c.core.Enabled(level)
}

func (c *filteredCore) With(fields []zapcore.Field) zapcore.Core {
	return &filteredCore{
		core:        c.core.With(filterFields(fields, c.allow)),
		allow:       c.allow,
		clearCaller: c.clearCaller,
	}
}

func (c *filteredCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

func (c *filteredCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if c.clearCaller {
		if !otelMessageAllowed(entry.Message) {
			return nil
		}
		entry.Caller = zapcore.EntryCaller{}
	}
	return c.core.Write(entry, filterFields(fields, c.allow))
}

func (c *filteredCore) Sync() error {
	return c.core.Sync()
}

func filterFields(fields []zapcore.Field, allow func(zapcore.Field) bool) []zapcore.Field {
	filtered := make([]zapcore.Field, 0, len(fields))
	for _, field := range fields {
		if allow(field) {
			filtered = append(filtered, field)
		}
	}
	return filtered
}

func stdoutFieldAllowed(field zapcore.Field) bool {
	return field.Key != otelContextField
}

func otelFieldAllowed(field zapcore.Field) bool {
	switch field.Key {
	case otelContextField,
		"addr",
		"duration",
		"error_type",
		"go_error_type",
		"method",
		"offset",
		"panic_type",
		"partition",
		"request_id",
		"route",
		"stack",
		"status",
		"topic":
		return true
	default:
		return false
	}
}

func otelMessageAllowed(message string) bool {
	switch message {
	case "Xray shutdown failed",
		"access Kafka partition fetch failed",
		"access Kafka record will retry",
		"access ordered event gap deferred",
		"access outbox publish failed",
		"billing outbox publish failed",
		"billing reconciliation failed",
		"billing webhook worker failed",
		"http handler panic",
		"http request",
		"http server listening",
		"node allocation reconciliation failed",
		"node allocation reconciliation reschedule failed",
		"node health persistence failed",
		"notification Kafka fetch failed",
		"notification delivery attempt failed",
		"notification ordered event gap deferred",
		"provisioning Kafka partition fetch failed",
		"provisioning Kafka record will retry",
		"provisioning command sequence gap deferred",
		"provisioning operation attempt failed",
		"provisioning outbox publish failed",
		"starting node agent",
		"starting service",
		"subscription Kafka partition fetch failed",
		"subscription Kafka record will retry",
		"subscription lifecycle transition failed",
		"subscription outbox publish failed",
		"subscription refund reconciliation failed",
		"telegram update dedupe completion failed",
		"telegram update dedupe release failed",
		"telegram update processing failed":
		return true
	default:
		return false
	}
}
