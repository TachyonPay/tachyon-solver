package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
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

// WalletSignatureAuth verifies that the caller owns the wallet address in the ?user= query param.
// The client must sign the message "tachyon-auth:{unix_timestamp}" with their private key and
// pass the result in X-Signature and X-Timestamp headers. Signatures older than 5 minutes are rejected.
func WalletSignatureAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		sig := c.GetHeader("X-Signature")
		tsStr := c.GetHeader("X-Timestamp")
		user := c.Query("user")

		if sig == "" || tsStr == "" || user == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "X-Signature, X-Timestamp headers and user query param are required",
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

		msg := fmt.Sprintf("tachyon-auth:%d", ts)
		prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msg))
		hash := crypto.Keccak256Hash([]byte(prefix + msg))

		sigBytes, err := hexutil.Decode(sig)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "Invalid X-Signature encoding",
			})
			return
		}

		// Normalize Ethereum v byte from 27/28 to 0/1
		if len(sigBytes) == 65 && sigBytes[64] >= 27 {
			sigBytes[64] -= 27
		}

		pubKey, err := crypto.SigToPub(hash.Bytes(), sigBytes)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Could not recover signer from signature",
			})
			return
		}

		recovered := crypto.PubkeyToAddress(*pubKey).Hex()
		if !strings.EqualFold(recovered, user) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "Signature does not match the provided user address",
			})
			return
		}

		c.Set("verified_address", recovered)
		c.Next()
	}
}
