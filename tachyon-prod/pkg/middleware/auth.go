package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/gin-gonic/gin"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
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

// WalletSignatureAuth recovers the caller's wallet address from the request signature.
// The client must sign "tachyon-auth:{unix_timestamp}" and pass the result in
// X-Signature and X-Timestamp headers. The recovered address is set in the gin context
// as "verified_address" for handlers to use. Signatures older than 30 minutes are rejected.
func WalletSignatureAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		sig := c.GetHeader("X-Signature")
		tsStr := c.GetHeader("X-Timestamp")

		if sig == "" || tsStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "X-Signature and X-Timestamp headers are required",
			})
			return
		}

		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "Invalid X-Timestamp value",
			})
			return
		}

		if time.Now().Unix()-ts > 1800 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Signature expired — timestamp is older than 30 minutes",
			})
			return
		}

		sigBytes, err := hexutil.Decode(sig)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "Invalid X-Signature encoding",
			})
			return
		}

		recovered, err := stealth.RecoverAuthSession(sigBytes, ts)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Could not recover signer from signature",
			})
			return
		}

		c.Set("verified_address", recovered.Hex())
		c.Next()
	}
}
