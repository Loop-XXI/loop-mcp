package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Tool registry
// ────────────────────────────────────────────────────────────────────────────

// Tool holds MCP metadata and the sats price used by the L402 middleware.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	SatsPrice   int64           `json:"-"`
}

// All returns the complete list of registered tools.
func All() []Tool {
	return []Tool{
		btcPriceTool(),
		btcSendDecisionTool(),
		lightningAddressResolveTool(),
		txDecodeExplainTool(),
	}
}

// ByName returns the tool with the given name, or an error if not found.
func ByName(name string) (Tool, error) {
	for _, t := range All() {
		if t.Name == name {
			return t, nil
		}
	}
	return Tool{}, fmt.Errorf("unknown tool: %s", name)
}

// ────────────────────────────────────────────────────────────────────────────
// Shared HTTP helper
// ────────────────────────────────────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 15 * time.Second}

func getJSON(url string, target interface{}) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("http get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("upstream %s returned %d: %s", url, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// ────────────────────────────────────────────────────────────────────────────
// Tool 1: btc_price
// ────────────────────────────────────────────────────────────────────────────

func btcPriceTool() Tool {
	return Tool{
		Name:        "btc_price",
		Description: "Get the current Bitcoin price in USD and major fiat currencies. Source: mempool.space. Real-time.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		SatsPrice:   10,
	}
}

func HandleBtcPrice(_ json.RawMessage) (interface{}, error) {
	var result map[string]interface{}
	if err := getJSON("https://mempool.space/api/v1/prices", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Tool 2: btc_send_decision
// ────────────────────────────────────────────────────────────────────────────

func btcSendDecisionTool() Tool {
	return Tool{
		Name: "btc_send_decision",
		Description: "Returns a composited send-or-wait recommendation for a Bitcoin transaction. " +
			"Fetches live mempool and fee data, then outputs a machine-actionable verdict " +
			"(SEND_NOW, WAIT, or URGENT_ONLY) with fee rates in sat/vB, mempool pressure level, " +
			"and estimated savings if you wait. Ideal for agents that need a single decision call " +
			"instead of parsing multiple raw mempool endpoints.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"urgency": {
					"type": "string",
					"enum": ["low","medium","high"],
					"description": "How time-sensitive is your transaction? low=economy, medium=default, high=next-block."
				}
			},
			"required": []
		}`),
		SatsPrice: 15,
	}
}

type mempoolFees struct {
	FastestFee  float64 `json:"fastestFee"`
	HalfHourFee float64 `json:"halfHourFee"`
	HourFee     float64 `json:"hourFee"`
	EconomyFee  float64 `json:"economyFee"`
	MinimumFee  float64 `json:"minimumFee"`
}

type mempoolInfo struct {
	Count        int         `json:"count"`
	Vsize        int         `json:"vsize"`
	TotalFee     float64     `json:"total_fee"`
	FeeHistogram [][]float64 `json:"fee_histogram"`
}

func HandleBtcSendDecision(params json.RawMessage) (interface{}, error) {
	var input struct {
		Urgency string `json:"urgency"`
	}
	_ = json.Unmarshal(params, &input)
	if input.Urgency == "" {
		input.Urgency = "medium"
	}

	var fees mempoolFees
	if err := getJSON("https://mempool.space/api/v1/fees/recommended", &fees); err != nil {
		return nil, fmt.Errorf("fees fetch: %w", err)
	}

	var memInfo mempoolInfo
	_ = getJSON("https://mempool.space/api/mempool", &memInfo) // best-effort

	vsizeMB := float64(memInfo.Vsize) / 1_000_000
	var pressure string
	switch {
	case vsizeMB < 5:
		pressure = "LOW"
	case vsizeMB < 50:
		pressure = "MEDIUM"
	case vsizeMB < 200:
		pressure = "HIGH"
	default:
		pressure = "CRITICAL"
	}

	var targetFee, waitFee float64
	var action, reason string
	switch input.Urgency {
	case "high":
		targetFee = fees.FastestFee
		waitFee = fees.HalfHourFee
	case "low":
		targetFee = fees.HourFee
		waitFee = fees.EconomyFee
	default: // medium
		targetFee = fees.HalfHourFee
		waitFee = fees.HourFee
	}

	premiumRatio := fees.FastestFee / (fees.EconomyFee + 0.001)
	switch {
	case pressure == "CRITICAL":
		action = "URGENT_ONLY"
		reason = fmt.Sprintf("Mempool is highly congested (%.0f MB backlog). Send only if time-critical; fastest fee is %.0f sat/vB.", vsizeMB, fees.FastestFee)
	case pressure == "LOW" && premiumRatio < 1.5:
		action = "SEND_NOW"
		reason = fmt.Sprintf("Mempool is clear and fees are flat (fastest %.0f, economy %.0f sat/vB). No savings from waiting.", fees.FastestFee, fees.EconomyFee)
	case premiumRatio >= 2.5:
		action = "WAIT"
		reason = fmt.Sprintf("Fastest fee (%.0f sat/vB) is %.1fx economy (%.0f sat/vB). Waiting could save significantly.", fees.FastestFee, premiumRatio, fees.EconomyFee)
	default:
		action = "SEND_NOW"
		reason = fmt.Sprintf("Fees are moderate. Sending at %.0f sat/vB is reasonable for %s priority.", targetFee, input.Urgency)
	}

	const typicalVbytes = 140
	estimatedSavings := int64((targetFee - waitFee) * typicalVbytes)
	if estimatedSavings < 0 {
		estimatedSavings = 0
	}

	return map[string]interface{}{
		"action":  action,
		"reason":  reason,
		"urgency": input.Urgency,
		"fee_rates": map[string]interface{}{
			"fastest_sat_vb":   fees.FastestFee,
			"half_hour_sat_vb": fees.HalfHourFee,
			"hour_sat_vb":      fees.HourFee,
			"economy_sat_vb":   fees.EconomyFee,
			"minimum_sat_vb":   fees.MinimumFee,
		},
		"mempool_pressure":               pressure,
		"mempool_vsize_mb":               vsizeMB,
		"estimated_savings_if_wait_sats": estimatedSavings,
		"note":                           "Savings estimate assumes a typical 140 vbyte transaction.",
	}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Tool 3: lightning_address_resolve
// ────────────────────────────────────────────────────────────────────────────

func lightningAddressResolveTool() Tool {
	return Tool{
		Name: "lightning_address_resolve",
		Description: "Resolve a Lightning Address (user@domain.com) to a payable BOLT11 invoice " +
			"for a given amount. Handles the full LNURL-pay protocol internally. " +
			"Returns the invoice plus min/max sendable amounts for validation.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"address": {
					"type": "string",
					"description": "Lightning Address in user@domain.com format."
				},
				"amount_sats": {
					"type": "integer",
					"description": "Amount you want to send, in satoshis.",
					"minimum": 1
				}
			},
			"required": ["address", "amount_sats"]
		}`),
		SatsPrice: 10,
	}
}

var lightningAddressRe = regexp.MustCompile(`^[^@]+@[^@]+\.[^@]+$`)

func HandleLightningAddressResolve(params json.RawMessage) (interface{}, error) {
	var input struct {
		Address    string `json:"address"`
		AmountSats int64  `json:"amount_sats"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if !lightningAddressRe.MatchString(input.Address) {
		return nil, fmt.Errorf("address must be in user@domain.com format")
	}
	if input.AmountSats < 1 {
		return nil, fmt.Errorf("amount_sats must be >= 1")
	}

	// Step 1: derive LNURL-pay endpoint from Lightning Address
	parts := strings.SplitN(input.Address, "@", 2)
	user, domain := parts[0], parts[1]
	lnurlEndpoint := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, user)

	// Step 2: fetch LNURL metadata
	var meta struct {
		Callback    string `json:"callback"`
		MinSendable int64  `json:"minSendable"` // millisats
		MaxSendable int64  `json:"maxSendable"` // millisats
		Tag         string `json:"tag"`
	}
	if err := getJSON(lnurlEndpoint, &meta); err != nil {
		return nil, fmt.Errorf("LNURL metadata fetch failed for %s: %w", input.Address, err)
	}
	if meta.Tag != "payRequest" {
		return nil, fmt.Errorf("unexpected LNURL tag: %s (expected payRequest)", meta.Tag)
	}

	amountMsats := input.AmountSats * 1000
	minSats := meta.MinSendable / 1000
	maxSats := meta.MaxSendable / 1000

	if amountMsats < meta.MinSendable {
		return nil, fmt.Errorf("amount %d sats is below minimum %d sats for %s", input.AmountSats, minSats, input.Address)
	}
	if meta.MaxSendable > 0 && amountMsats > meta.MaxSendable {
		return nil, fmt.Errorf("amount %d sats exceeds maximum %d sats for %s", input.AmountSats, maxSats, input.Address)
	}

	// Step 3: request invoice from callback
	callbackURL := meta.Callback
	sep := "?"
	if strings.Contains(callbackURL, "?") {
		sep = "&"
	}
	callbackURL += fmt.Sprintf("%samount=%d", sep, amountMsats)

	var invoiceResp struct {
		PR     string        `json:"pr"`
		Routes []interface{} `json:"routes"`
	}
	if err := getJSON(callbackURL, &invoiceResp); err != nil {
		return nil, fmt.Errorf("LNURL invoice callback failed: %w", err)
	}
	if invoiceResp.PR == "" {
		return nil, fmt.Errorf("LNURL callback did not return an invoice")
	}

	return map[string]interface{}{
		"bolt11":            invoiceResp.PR,
		"address":           input.Address,
		"amount_sats":       input.AmountSats,
		"min_sendable_sats": minSats,
		"max_sendable_sats": maxSats,
		"note":              "Invoice is ready to pay. Verify amount before paying.",
	}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Tool 4: tx_decode_explain
// ────────────────────────────────────────────────────────────────────────────

func txDecodeExplainTool() Tool {
	return Tool{
		Name: "tx_decode_explain",
		Description: "Fetch a Bitcoin transaction by txid and return a structured, agent-ready summary: " +
			"type (P2WPKH/P2TR/P2SH/etc.), input/output counts, fees, fee rate in sat/vB, " +
			"confirmation status, RBF flag, SegWit/Taproot flags, and a one-line agent_summary " +
			"string ready for LLM context injection. Saves 500-2000 tokens vs parsing raw TX JSON.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"txid": {
					"type": "string",
					"description": "Bitcoin transaction ID (64 hex characters).",
					"minLength": 64,
					"maxLength": 64
				}
			},
			"required": ["txid"]
		}`),
		SatsPrice: 25,
	}
}

type mempoolTx struct {
	Txid   string `json:"txid"`
	Status struct {
		Confirmed   bool   `json:"confirmed"`
		BlockHeight int    `json:"block_height"`
		BlockHash   string `json:"block_hash"`
		BlockTime   int64  `json:"block_time"`
	} `json:"status"`
	Fee  int64 `json:"fee"`
	Vin  []struct {
		Sequence   uint32   `json:"sequence"`
		Witness    []string `json:"witness"`
		IsCoinbase bool     `json:"is_coinbase"`
		Prevout    struct {
			Scriptpubkey     string `json:"scriptpubkey"`
			ScriptpubkeyType string `json:"scriptpubkey_type"`
			Value            int64  `json:"value"`
		} `json:"prevout"`
	} `json:"vin"`
	Vout []struct {
		Value            int64  `json:"value"`
		Scriptpubkey     string `json:"scriptpubkey"`
		ScriptpubkeyType string `json:"scriptpubkey_type"`
	} `json:"vout"`
	Size   int `json:"size"`
	Weight int `json:"weight"`
}

func HandleTxDecodeExplain(params json.RawMessage) (interface{}, error) {
	var input struct {
		Txid string `json:"txid"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	txid := strings.TrimSpace(input.Txid)
	if len(txid) != 64 {
		return nil, fmt.Errorf("txid must be 64 hex characters")
	}

	var tx mempoolTx
	if err := getJSON("https://mempool.space/api/tx/"+txid, &tx); err != nil {
		return nil, fmt.Errorf("tx fetch: %w", err)
	}

	// vsize = ceil(weight / 4)
	vsize := (tx.Weight + 3) / 4
	if vsize == 0 {
		vsize = tx.Size
	}

	var totalIn int64
	for _, vin := range tx.Vin {
		totalIn += vin.Prevout.Value
	}
	var totalOut int64
	for _, vout := range tx.Vout {
		totalOut += vout.Value
	}

	fee := tx.Fee
	if fee == 0 && totalIn > 0 {
		fee = totalIn - totalOut
	}

	var feeRateSvb float64
	if vsize > 0 && fee > 0 {
		feeRateSvb = float64(fee) / float64(vsize)
	}

	segwit := false
	taproot := false
	outputTypes := map[string]int{}
	for _, vout := range tx.Vout {
		t := vout.ScriptpubkeyType
		outputTypes[t]++
		if strings.Contains(t, "v0_p2") || strings.Contains(t, "p2wpkh") || strings.Contains(t, "p2wsh") {
			segwit = true
		}
		if strings.Contains(t, "v1_p2tr") || t == "p2tr" {
			taproot = true
			segwit = true
		}
	}
	for _, vin := range tx.Vin {
		if len(vin.Witness) > 0 {
			segwit = true
		}
	}

	dominantType := "unknown"
	maxCount := 0
	for t, count := range outputTypes {
		if count > maxCount {
			maxCount = count
			dominantType = t
		}
	}
	if taproot {
		dominantType = "P2TR (Taproot)"
	} else if strings.Contains(dominantType, "p2wpkh") {
		dominantType = "P2WPKH (Native SegWit)"
	} else if strings.Contains(dominantType, "p2wsh") {
		dominantType = "P2WSH (SegWit Script)"
	} else if strings.Contains(dominantType, "p2sh") {
		dominantType = "P2SH"
	} else if strings.Contains(dominantType, "p2pkh") {
		dominantType = "P2PKH (Legacy)"
	}

	rbfEnabled := false
	isCoinbase := false
	for _, vin := range tx.Vin {
		if vin.Sequence < 0xFFFFFFFE {
			rbfEnabled = true
		}
		if vin.IsCoinbase {
			isCoinbase = true
		}
	}

	confirmStatus := "unconfirmed"
	if tx.Status.Confirmed {
		confirmStatus = fmt.Sprintf("confirmed at block %d", tx.Status.BlockHeight)
	}

	txCategory := classifyTx(len(tx.Vin), len(tx.Vout), isCoinbase)
	agentSummary := fmt.Sprintf(
		"%s Bitcoin transaction (%s), %d inputs -> %d outputs, fee %.1f sat/vB, %s.",
		txCategory, dominantType, len(tx.Vin), len(tx.Vout), feeRateSvb, confirmStatus,
	)

	feeRateStr := strconv.FormatFloat(feeRateSvb, 'f', 2, 64)

	return map[string]interface{}{
		"txid":                txid,
		"type":                dominantType,
		"input_count":         len(tx.Vin),
		"output_count":        len(tx.Vout),
		"total_input_sats":    totalIn,
		"total_output_sats":   totalOut,
		"fee_sats":            fee,
		"fee_rate_svb":        feeRateStr,
		"vsize_vbytes":        vsize,
		"confirmation_status": confirmStatus,
		"rbf_enabled":         rbfEnabled,
		"segwit":              segwit,
		"taproot":             taproot,
		"is_coinbase":         isCoinbase,
		"agent_summary":       agentSummary,
	}, nil
}

func classifyTx(inputCount, outputCount int, isCoinbase bool) string {
	switch {
	case isCoinbase:
		return "Coinbase"
	case inputCount == 1 && outputCount == 1:
		return "Simple transfer"
	case inputCount == 1 && outputCount == 2:
		return "Payment with change"
	case inputCount > 5 && outputCount > 5:
		return "Batch payment"
	case inputCount > 3 && outputCount == 1:
		return "UTXO consolidation"
	case inputCount >= 2 && outputCount >= 2 && inputCount == outputCount:
		return "Possible CoinJoin"
	default:
		return "Standard"
	}
}