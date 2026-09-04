package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// StarsCurrency is Telegram's currency code for Stars. An invoice priced in it
// carries no provider token and settles inside Telegram, which is the whole
// reason it can be opened straight from a Mini App.
const StarsCurrency = "XTR"

// Bot is the slice of the Bot API this game needs: Stars invoices, and the
// updates that report what happened to them.
type Bot struct {
	token  string
	client *http.Client
}

// NewBot builds a client. It carries no timeout of its own -- long polling
// holds a request open for as long as it is told to, so the deadline belongs
// to the caller's context.
func NewBot(token string) *Bot {
	return &Bot{token: token, client: &http.Client{}}
}

// APIError is Telegram refusing a call. It is worth keeping whole: the
// description is the only thing that says which argument it disliked.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %s: %s (code %d)", e.Method, e.Description, e.Code)
}

// Invoice is one Stars charge, before it becomes a link.
type Invoice struct {
	Title       string
	Description string
	// Payload comes back untouched on the successful payment, which is how a
	// charge is tied to the player who started it.
	Payload string
	Stars   int64
}

// CreateInvoiceLink turns a charge into a link the Mini App can open with
// WebApp.openInvoice.
func (b *Bot) CreateInvoiceLink(ctx context.Context, in Invoice) (string, error) {
	body := map[string]any{
		"title":       in.Title,
		"description": in.Description,
		"payload":     in.Payload,
		"currency":    StarsCurrency,
		"prices":      []map[string]any{{"label": in.Title, "amount": in.Stars}},
	}

	var link string
	if err := b.call(ctx, "createInvoiceLink", body, &link); err != nil {
		return "", err
	}
	return link, nil
}

// PreCheckoutQuery is Telegram asking whether a charge may go ahead. It must be
// answered within ten seconds or the payment fails on the player's screen.
type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

// SuccessfulPayment is the money actually having moved.
type SuccessfulPayment struct {
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
	// ChargeID identifies this payment for the rest of time. Telegram may
	// deliver the same update twice, so it is what makes crediting idempotent.
	ChargeID string `json:"telegram_payment_charge_id"`
}

type Message struct {
	From              *User              `json:"from"`
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment"`
}

type Update struct {
	ID               int64             `json:"update_id"`
	Message          *Message          `json:"message"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query"`
}

// GetUpdates long-polls for the next batch after offset.
//
// Polling rather than a webhook on purpose: a webhook needs a stable public
// URL, and this game is developed behind tunnels whose address changes every
// run.
func (b *Bot) GetUpdates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error) {
	body := map[string]any{
		"offset":  offset,
		"timeout": int(wait.Seconds()),
		// Anything else would be answered and dropped; asking for less keeps
		// the bot from consuming updates another consumer may want.
		"allowed_updates": []string{"message", "pre_checkout_query"},
	}

	// Telegram returns just after `wait`; the margin covers the round trip.
	ctx, cancel := context.WithTimeout(ctx, wait+30*time.Second)
	defer cancel()

	var updates []Update
	if err := b.call(ctx, "getUpdates", body, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// AnswerPreCheckoutQuery approves or refuses a charge. reason is shown to the
// player and is only read when ok is false.
func (b *Bot) AnswerPreCheckoutQuery(ctx context.Context, id string, ok bool, reason string) error {
	body := map[string]any{"pre_checkout_query_id": id, "ok": ok}
	if !ok {
		body["error_message"] = reason
	}
	return b.call(ctx, "answerPreCheckoutQuery", body, nil)
}

func (b *Bot) call(ctx context.Context, method string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("telegram: encoding %s: %w", method, err)
	}

	url := "https://api.telegram.org/bot" + b.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram: building %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: calling %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do about a failed close

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("telegram: reading %s (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if !envelope.OK {
		return &APIError{Method: method, Code: envelope.ErrorCode, Description: envelope.Description}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("telegram: decoding the result of %s: %w", method, err)
	}
	return nil
}
