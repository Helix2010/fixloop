package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Pagination struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func OKPaged(c *gin.Context, data any, p Pagination) {
	c.JSON(http.StatusOK, gin.H{"data": data, "pagination": p})
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"data": data})
}

func Err(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": ErrorBody{Code: code, Message: message}})
}

func NotFound(c *gin.Context, what string) {
	Err(c, http.StatusNotFound, "NOT_FOUND", what+"不存在")
}

func BadRequest(c *gin.Context, message string) {
	Err(c, http.StatusBadRequest, "BAD_REQUEST", message)
}

func Internal(c *gin.Context) {
	Err(c, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误")
}
