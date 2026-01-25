package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

func APIKeyAuth() gin.HandlerFunc {
	apiKey := os.Getenv("ADMIN_API_KEY")

	return func(c *gin.Context) {
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"error":   "Admin API key not configured",
			})
			return
		}

		providedKey := c.GetHeader("X-API-Key")
		if providedKey == "" {
			providedKey = c.Query("api_key")
		}

		if providedKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "API key required",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(providedKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "Invalid API key",
			})
			return
		}

		c.Next()
	}
}

func GenerateAPIKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
