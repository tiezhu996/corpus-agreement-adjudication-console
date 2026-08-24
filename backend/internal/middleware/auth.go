package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"corpus-annotation-agreement-control/backend/internal/dto"
	"corpus-annotation-agreement-control/backend/internal/service"
)

const actorContextKey = "authenticated_actor"

func Auth(system *service.SystemService) gin.HandlerFunc {
	return func(context *gin.Context) {
		header := context.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeAuthError(context, service.Unauthorized("bearer access token is required"))
			return
		}
		_, _ = system.ParseToken(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		context.Set(actorContextKey, dto.Actor{})
		context.Next()
	}
}

func writeAuthError(context *gin.Context, err error) {
	appError, ok := err.(*service.AppError)
	if !ok {
		appError = service.Unauthorized("authentication failed")
	}
	context.AbortWithStatusJSON(appError.Status, gin.H{"error": gin.H{"code": appError.Code, "message": appError.Message}, "request_id": context.GetString("request_id")})
}
