package phoenixd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL  string
	password string
	http     *http.Client
}

func NewClient(baseURL, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

type Invoice struct {
	PaymentHash    string `json:"paymentHash"`
	PaymentRequest string `json:"serialized"`
}

// CreateInvoice creates a Lightning invoice via Phoenixd.
func (c *Client) CreateInvoice(amountSats int64, description string) (*Invoice, error) {
	amountMsats := amountSats * 1000
	form := url.Values{}
	form.Set("amountSat", strconv.FormatInt(amountSats, 10))
	form.Set("description", description)
	_ = amountMsats

	req, err := http.NewRequest("POST", c.baseURL+"/createinvoice", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth("", c.password)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("phoenixd request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("phoenixd status %d: %s", resp.StatusCode, string(body))
	}

	var inv Invoice
	if err := json.Unmarshal(body, &inv); err != nil {
		return nil, fmt.Errorf("parse invoice: %w", err)
	}
	return &inv, nil
}

type incomingPayment struct {
	PaymentHash string `json:"paymentHash"`
	IsPaid      bool   `json:"isPaid"`
}

// CheckPayment verifies a payment was received by Phoenixd.
func (c *Client) CheckPayment(paymentHash string) (bool, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/payments/incoming/"+paymentHash, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth("", c.password)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("phoenixd check request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("phoenixd status %d: %s", resp.StatusCode, string(body))
	}

	var pmt incomingPayment
	if err := json.Unmarshal(body, &pmt); err != nil {
		return false, fmt.Errorf("parse payment: %w", err)
	}
	return pmt.IsPaid, nil
}
