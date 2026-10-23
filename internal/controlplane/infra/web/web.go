package web

import (
	"context"
	"errors"
	"fmt"
	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/controlplane/infra/web/handler/auth"
	"go-software-factory/internal/controlplane/infra/web/middleware"
	appauth "go-software-factory/internal/engine/app/auth"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
)

const (
	startTimeout      = 2 * time.Second
	readHeaderTimeout = 5 * time.Second
)

type WebServer struct {
	server *http.Server
}

type WebConfig struct {
	WebServerPort int
	AuthSecretKey string
	Production    bool
}

func NewWebServer(
	cfg *WebConfig,
	middlewares []middleware.Middleware,
	privateHandlers []handler.Handler,
	loginUseCase *appauth.LoginUseCase,
) *WebServer {
	r := gin.New()
	r.HTMLRender = newRenderer()

	for _, m := range middlewares {
		r.Use(m.Handler())
	}
	store := cookie.NewStore([]byte(cfg.AuthSecretKey))
	store.Options(sessions.Options{
		Path: "/", MaxAge: 30 * 24 * 60 * 60, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: cfg.Production,
	})
	r.Use(sessions.Sessions("factory-session", store))
	// FormValue in gin-csrf ignores parsing errors and caches partial form data.
	// Reject malformed forms before it can discard an execution input.
	r.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodPost {
			if err := c.Request.ParseForm(); err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid form encoding"})

				return
			}
		}
		c.Next()
	})
	r.Use(csrf.Middleware(csrf.Options{
		Secret: cfg.AuthSecretKey,
		ErrorFunc: func(c *gin.Context) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "CSRF token mismatch"})
		},
	}))
	registerHandlers(r.Group(""), []handler.Handler{
		auth.NewLoginView(),
		auth.NewLogin(loginUseCase),
		auth.NewDefaultRoute(),
	})
	registerHandlers(r.Group("", middleware.AuthRequired()), privateHandlers)

	return &WebServer{
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.WebServerPort),
			Handler:           r.Handler(),
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}
}

func registerHandlers(root *gin.RouterGroup, handlers []handler.Handler) {
	groups := make(map[string]*gin.RouterGroup)
	for _, routeHandler := range handlers {
		group := groups[routeHandler.Group()]
		if group == nil {
			group = root.Group(routeHandler.Group())
			groups[routeHandler.Group()] = group
		}
		group.Handle(routeHandler.Method(), routeHandler.Path(), routeHandler.Handle)
	}
}

func (w *WebServer) Start(ctx context.Context) error {
	ch := make(chan error)

	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()

	go func() {
		defer close(ch)

		if err := w.server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			slog.With("err", err).Error("Failed to start server")
			ch <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.With("address", w.server.Addr).Info("web server started")
	case err := <-ch:
		return err
	}

	return nil
}

func (w *WebServer) Stop(ctx context.Context) error {
	if err := w.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shut down web server: %w", err)
	}

	return nil
}

func (w *WebServer) Name() string {
	return "web-server"
}
