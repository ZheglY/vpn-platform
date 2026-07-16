package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type Config struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

func DefaultConfig() Config {
	return Config{
		Addr:              ":8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   10 * time.Second,
	}
}

func New(cfg Config, handler http.Handler) *http.Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}

func Run(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration, logger *zap.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", zap.String("addr", srv.Addr))
		if srv.TLSConfig != nil {
			errCh <- srv.ListenAndServeTLS("", "")
			return
		}
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen and serve: %w", err)
	case <-ctx.Done():
		if shutdownTimeout <= 0 {
			shutdownTimeout = 10 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
