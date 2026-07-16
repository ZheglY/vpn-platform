package httpserver

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"go.uber.org/zap"

	"github.com/yarik/vpn-service/internal/platform/httperror"
	"github.com/yarik/vpn-service/internal/platform/requestid"
)

type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestid.Header)
		if !requestid.Valid(id) {
			id = requestid.New()
		}
		w.Header().Set(requestid.Header, id)
		next.ServeHTTP(w, r.WithContext(requestid.WithContext(r.Context(), id)))
	})
}

func LimitBody(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		if maxBytes <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func Recover(logger *zap.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("http handler panic",
						zap.String("request_id", requestid.FromRequest(r)),
						zap.String("panic_type", fmt.Sprintf("%T", recovered)),
						zap.ByteString("stack", debug.Stack()),
					)
					httperror.Write(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func LogRequests(logger *zap.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			fields := []zap.Field{
				zap.String("request_id", requestid.FromRequest(r)),
				zap.String("method", r.Method),
				zap.Int("status", rec.status),
				zap.Duration("duration", time.Since(started)),
			}
			if r.Pattern != "" {
				fields = append(fields, zap.String("route", r.Pattern))
			}
			logger.Info("http request", fields...)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
