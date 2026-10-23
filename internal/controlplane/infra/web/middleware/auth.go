package middleware

import (
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const SessionParamUsername = "username"

func SessionUsername(c *gin.Context) string {
	username, _ := sessions.Default(c).Get(SessionParamUsername).(string)

	return username
}

func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if SessionUsername(c) == "" {
			if c.GetHeader("HX-Request") == "true" {
				c.Header("HX-Redirect", "/auth/login")
				c.Status(http.StatusUnauthorized)
			} else {
				c.Redirect(http.StatusFound, "/auth/login")
			}
			c.Abort()

			return
		}
		c.Next()
	}
}
