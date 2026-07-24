package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type openConfig struct {
	registerer prometheus.Registerer
	service    string
}

type Option func(*openConfig) error

func WithMetrics(registerer prometheus.Registerer, service string) Option {
	return func(config *openConfig) error {
		if registerer == nil || service == "" {
			return fmt.Errorf("postgres metrics registerer and service are required")
		}
		config.registerer = registerer
		config.service = service
		return nil
	}
}

func OpenPool(ctx context.Context, dsn string, options ...Option) (*pgxpool.Pool, error) {
	var openCfg openConfig
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("postgres option is nil")
		}
		if err := option(&openCfg); err != nil {
			return nil, err
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	if openCfg.registerer != nil {
		cfg.ConnConfig.Tracer = newQueryMetrics(openCfg.registerer, openCfg.service)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if openCfg.registerer != nil {
		registerer := prometheus.WrapRegistererWith(prometheus.Labels{"service": openCfg.service}, openCfg.registerer)
		if err := registerer.Register(newPoolCollector(pool)); err != nil {
			pool.Close()
			return nil, fmt.Errorf("register postgres pool metrics: %w", err)
		}
	}
	return pool, nil
}
