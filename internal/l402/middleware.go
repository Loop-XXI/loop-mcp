package l402

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/Loop-XXI/loop-mcp/internal/phoenixd"
)

// invoiceStore is an in-memory store mapping paymentHash → preimage (once verified)
type invoiceStore struct {
	mu   sync.RWMutex
	paid map[string]bool
}

// Middleware implements the L402 payment gate.
type Middleware struct {
	pd        *phoenixd.Client
	priceSats int64
	store     *invoiceStore
}

func NewMiddleware(pd *phoenixd.Client, priceSats int64) *Middleware {
	return &Middleware{
		pd:        pd,
		priceSats: priceSats,
		store:     &invoiceStore{paid: make(map[string]bool)},
	}
}

// Gate returns a Gin middleware handler enforcing L402 payment.
func (m *Middleware) Gate() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		if strings.HasPrefix(authHeader, "L402 ") {
			token := strings.TrimPrefix(authHeader, "L402 ")
			parts := strings.SplitN(token, ":", 2)
			if len(parts) != 2 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid L402 token format — expected L402 <hash>:<preimage>"})
				return
			}
			paymentHash := parts[0]
			preimage := parts[1]

			// 1. Local SHA256 verification
			preimageBytes, err := hex.DecodeString(preimage)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "preimage must be hex-encoded"})
				return
			}
			computed := sha256.Sum256(preimageBytes)
			computedHex := hex.EncodeToString(computed[:])
			if computedHex != strings.ToLower(paymentHash) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "preimage does not match payment hash"})
				return
			}

			// 2. Cross-check with Phoenixd (fail-open if Phoenixd is unreachable)
			m.store.mu.Lock()
			if !m.store.paid[paymentHash] {
				ok, err := m.pd.CheckPayment(paymentHash)
				if err == nil && ok {
					m.store.paid[paymentHash] = true
				} else if err == nil && !ok {
					m.store.mu.Unlock()
					c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
						"code":    402,
						"message": "invoice not paid yet — please complete payment first",
					})
					return
				}
				// err != nil → Phoenixd unreachable → fail-open (SHA256 already passed)
			}
			m.store.mu.Unlock()

			c.Next()
			return
		}

		// No auth — issue L402 challenge
		inv, err := m.pd.CreateInvoice(m.priceSats, "loop-mcp tool call")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": fmt.Sprintf("could not create Lightning invoice: %v", err),
			})
			return
		}

		c.Header("WWW-Authenticate", fmt.Sprintf(`L402 macaroon="%s",invoice="%s"`, inv.PaymentHash, inv.PaymentRequest))
		c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
			"code":         402,
			"message":      fmt.Sprintf("Payment required: %d sats via Lightning", m.priceSats),
			"payment_hash": inv.PaymentHash,
			"invoice":      inv.PaymentRequest,
			"instructions": "Pay the Lightning invoice, then retry with: Authorization: L402 <hash>:<preimage>",
		})
	}
}
