package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type loggerMiddleware struct {
}

func NewLoggerMiddleware() Middleware {
	return &loggerMiddleware{}
}

func (l *loggerMiddleware) Handler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		started := time.Now()
		method := ctx.Request.Method
		path := ctx.Request.URL.Path

		ctx.Next()

		status := ctx.Writer.Status()
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.String("method", method),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(started)),
			slog.String("client_ip", ctx.ClientIP()),
			slog.Int("response_size", ctx.Writer.Size()),
		}
		if len(ctx.Errors) > 0 {
			attrs = append(attrs, slog.String("errors", ctx.Errors.String()))
		}

		slog.LogAttrs(ctx.Request.Context(), level, "HTTP request", attrs...)
	}
}
