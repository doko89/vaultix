package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/vaultix/vaultix/internal/domain"
)

// WebhookEndpoint is one named outbound webhook target. URLs (which often
// carry signing tokens) live in manager config — never in the database —
// and are never written to logs; only the endpoint name is logged.
type WebhookEndpoint struct {
	// URL is the POST target. Query strings and userinfo are stripped from
	// every log line and error message.
	URL string
	// Headers are extra request headers (e.g. X-Api-Key name; values are
	// never logged).
	Headers map[string]string
	// Secret, when set, adds an X-Vaultix-Signature header (hex HMAC-SHA256
	// of the request body) so receivers can authenticate the Manager.
	Secret string
}

// WebhookConfig configures the webhook sender.
type WebhookConfig struct {
	Endpoints map[string]WebhookEndpoint
	// Timeout bounds one POST attempt (connect + headers + body).
	Timeout time.Duration
	// MaxTries caps total attempts (1 = no retry). Transient failures
	// (network errors, 429, 5xx) retry with exponential backoff; other 4xx
	// responses fail fast without retry.
	MaxTries        int
	InitialInterval time.Duration
	MaxInterval     time.Duration
}

func (c WebhookConfig) withDefaults() WebhookConfig {
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.MaxTries <= 0 {
		c.MaxTries = 3
	}
	if c.InitialInterval <= 0 {
		c.InitialInterval = 500 * time.Millisecond
	}
	if c.MaxInterval <= 0 {
		c.MaxInterval = 10 * time.Second
	}
	if c.Endpoints == nil {
		c.Endpoints = map[string]WebhookEndpoint{}
	}
	return c
}

// webhookPayload is the POST body delivered to endpoints.
type webhookPayload struct {
	Event     string `json:"event"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Recipient string `json:"recipient"`
	RefType   string `json:"ref_type"`
	RefID     string `json:"ref_id"`
	Attempt   int    `json:"attempt"`
	SentAt    string `json:"sent_at"`
}

// WebhookSender POSTs notifications to named endpoints. It implements Sender.
type WebhookSender struct {
	cfg    WebhookConfig
	client *http.Client
	log    *slog.Logger
	now    func() time.Time
}

// NewWebhookSender builds a sender. client may be nil (a default client with
// cfg.Timeout is used). log may be nil (slog.Default).
func NewWebhookSender(cfg WebhookConfig, client *http.Client, log *slog.Logger) (*WebhookSender, error) {
	cfg = cfg.withDefaults()
	for name, ep := range cfg.Endpoints {
		u, err := url.Parse(strings.TrimSpace(ep.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("webhook endpoint %q: invalid URL", name)
		}
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	if log == nil {
		log = slog.Default()
	}
	return &WebhookSender{cfg: cfg, client: client, log: log, now: time.Now}, nil
}

// Channel implements Sender.
func (s *WebhookSender) Channel() domain.NotificationChannel { return domain.NotificationWebhook }

// Send delivers n to the endpoint named by n.Recipient.
func (s *WebhookSender) Send(ctx context.Context, n *domain.Notification) error {
	ep, ok := s.cfg.Endpoints[n.Recipient]
	if !ok {
		return fmt.Errorf("webhook: unknown endpoint %q", n.Recipient)
	}
	return s.post(ctx, n.Recipient, ep, eventOf(n), n)
}

// Test posts a test message to an arbitrary URL without persisting anything.
// It exists so the notifications API can verify a URL before an operator
// stores it in configuration.
func (s *WebhookSender) Test(ctx context.Context, rawURL, message string) error {
	ep := WebhookEndpoint{URL: rawURL}
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: invalid webhook URL", domain.ErrValidation)
	}
	n := &domain.Notification{Subject: "Vaultix webhook test", Body: message, Recipient: "test"}
	return s.post(ctx, "test", ep, "test", n)
}

func eventOf(n *domain.Notification) string {
	if n.RefType != "" {
		return n.RefType + ":" + n.RefID
	}
	return "notification"
}

func (s *WebhookSender) post(ctx context.Context, name string, ep WebhookEndpoint, event string, n *domain.Notification) error {
	body, err := json.Marshal(webhookPayload{
		Event:     event,
		Subject:   n.Subject,
		Body:      n.Body,
		Recipient: n.Recipient,
		RefType:   n.RefType,
		RefID:     n.RefID,
		Attempt:   n.Attempts + 1,
		SentAt:    s.now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("webhook %q: encode payload: %w", name, err)
	}
	exp := backoff.NewExponentialBackOff()
	exp.InitialInterval = s.cfg.InitialInterval
	exp.MaxInterval = s.cfg.MaxInterval
	_, err = backoff.Retry(ctx, func() (struct{}, error) {
		return struct{}{}, s.tryPost(ctx, name, ep, body)
	}, backoff.WithBackOff(exp), backoff.WithMaxTries(uint(s.cfg.MaxTries)),
		backoff.WithNotify(func(err error, d time.Duration) {
			// Redacted: endpoint name + sanitized error only.
			s.log.Warn("webhook delivery retrying",
				"endpoint", name, "error", sanitizeWebhookError(err, ep.URL), "backoff", d.String())
		}))
	if err != nil {
		return fmt.Errorf("webhook %q: %w", name, sanitizeWebhookError(err, ep.URL))
	}
	s.log.Info("webhook delivered", "endpoint", name, "subject", n.Subject)
	return nil
}

func (s *WebhookSender) tryPost(ctx context.Context, name string, ep WebhookEndpoint, body []byte) error {
	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return backoff.Permanent(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vaultix-manager/1.0")
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}
	if ep.Secret != "" {
		mac := hmac.New(sha256.New, []byte(ep.Secret))
		mac.Write(body)
		req.Header.Set("X-Vaultix-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST: %w", err) // transient: retry
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("endpoint returned %s", resp.Status) // transient: retry
	default:
		// 4xx (other than 429): the request is bad; retrying is pointless.
		return backoff.Permanent(fmt.Errorf("endpoint rejected payload: %s", resp.Status))
	}
}

// sanitizeWebhookError removes the raw endpoint URL (query/userinfo may carry
// tokens) from err, replacing it with a redacted host-only form.
func sanitizeWebhookError(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if rawURL != "" && strings.Contains(msg, rawURL) {
		msg = strings.ReplaceAll(msg, rawURL, redactURL(rawURL))
	}
	return fmt.Errorf("%s", msg)
}

// redactURL strips userinfo, query, and fragment from raw.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
