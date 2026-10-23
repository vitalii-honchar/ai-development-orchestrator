package handler

import "github.com/gin-gonic/gin"

type Handler interface {
	Group() string
	Method() string
	Path() string
	Handle(*gin.Context)
}
