package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/controlplane/infra/web/middleware"
	appauth "go-software-factory/internal/engine/app/auth"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
)

const CatalogPath = "/process-definitions/"

type loginView struct{}

func NewLoginView() handler.Handler {
	return &loginView{}
}

func (l *loginView) Group() string {
	return "auth"
}

func (l *loginView) Method() string {
	return http.MethodGet
}

func (l *loginView) Path() string {
	return "/login"
}

func (l *loginView) Handle(c *gin.Context) {
	if middleware.SessionUsername(c) != "" {
		c.Redirect(http.StatusFound, CatalogPath)

		return
	}
	renderLogin(c, http.StatusOK, "")
}

type login struct {
	useCase *appauth.LoginUseCase
}

func NewLogin(useCase *appauth.LoginUseCase) handler.Handler {
	return &login{useCase: useCase}
}

func (l *login) Group() string {
	return "auth"
}

func (l *login) Method() string {
	return http.MethodPost
}

func (l *login) Path() string {
	return "/login"
}

func (l *login) Handle(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	username, err := l.useCase.Login(ctx, c.PostForm("username"), c.PostForm("password"))
	if errors.Is(err, appauth.ErrInvalidCredentials) {
		if c.GetHeader("HX-Request") == "true" {
			c.HTML(http.StatusUnauthorized, "auth/error", gin.H{"Error": "Invalid username or password"})
		} else {
			renderLogin(c, http.StatusUnauthorized, "Invalid username or password")
		}

		return
	}
	if err != nil {
		slog.Error("Login failed", "err", err)
		c.String(http.StatusInternalServerError, "Unable to sign in")

		return
	}
	session := sessions.Default(c)
	session.Clear()
	session.Set(middleware.SessionParamUsername, username)
	if err := session.Save(); err != nil {
		slog.Error("Save login session failed", "err", err)
		c.String(http.StatusInternalServerError, "Unable to sign in")

		return
	}
	if c.GetHeader("HX-Request") == "true" {
		c.Header("HX-Redirect", CatalogPath)
		c.Status(http.StatusOK)
	} else {
		c.Redirect(http.StatusSeeOther, CatalogPath)
	}
}

func renderLogin(c *gin.Context, status int, message string) {
	token := csrf.GetToken(c)
	// gin-csrf ignores save errors internally. Explicitly check before rendering.
	if err := sessions.Default(c).Save(); err != nil {
		c.String(http.StatusInternalServerError, "Unable to sign in")

		return
	}
	c.HTML(status, "auth/login", gin.H{"CSRFToken": token, "Error": message, "LoginPage": true})
}

type defaultRoute struct{}

func NewDefaultRoute() handler.Handler {
	return &defaultRoute{}
}

func (d *defaultRoute) Group() string {
	return ""
}

func (d *defaultRoute) Method() string {
	return http.MethodGet
}

func (d *defaultRoute) Path() string {
	return "/"
}

func (d *defaultRoute) Handle(c *gin.Context) {
	if middleware.SessionUsername(c) == "" {
		c.Redirect(http.StatusFound, "/auth/login")

		return
	}
	c.Redirect(http.StatusFound, CatalogPath)
}
