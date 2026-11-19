package notification

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/vaultix/vaultix/internal/domain"
)

// SMTP auth and TLS modes.
const (
	SMTPAuthNone  = "none"
	SMTPAuthPlain = "plain"
	SMTPAuthLogin = "login"

	SMTPTLSNone     = "none"
	SMTPTLSSTARTTLS = "starttls"
	SMTPTLSImplicit = "implicit"
)

// SMTPConfig configures the SMTP sender (stdlib net/smtp).
type SMTPConfig struct {
	Host string
	Port int
	// From is the envelope sender and From: header.
	From string
	// Auth is none|plain|login. Credentials are refused over cleartext
	// connections unless the host is local (loopback).
	Auth     string
	Username string
	Password string
	// TLS is none|starttls|implicit. With starttls the session upgrades via
	// STARTTLS when the server advertises it (required when auth is set).
	TLS string
	// DialTimeout bounds TCP/TLS connect; IOTimeout bounds the SMTP
	// conversation. TLSConfig overrides TLS defaults (ServerName is always
	// set to Host when nil).
	DialTimeout time.Duration
	IOTimeout   time.Duration
	TLSConfig   *tls.Config
	// MaxTries caps total attempts (1 = no retry) with exponential backoff
	// between transient failures.
	MaxTries        int
	InitialInterval time.Duration
	MaxInterval     time.Duration
}

func (c SMTPConfig) withDefaults() SMTPConfig {
	if c.Port == 0 {
		c.Port = 587
	}
	if c.Auth == "" {
		c.Auth = SMTPAuthNone
	}
	if c.TLS == "" {
		c.TLS = SMTPTLSSTARTTLS
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 10 * time.Second
	}
	if c.IOTimeout <= 0 {
		c.IOTimeout = 30 * time.Second
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
	return c
}

func (c SMTPConfig) validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("smtp: host is required")
	}
	if strings.TrimSpace(c.From) == "" || !strings.Contains(c.From, "@") {
		return fmt.Errorf("smtp: valid from address is required")
	}
	switch c.Auth {
	case SMTPAuthNone, SMTPAuthPlain, SMTPAuthLogin:
	default:
		return fmt.Errorf("smtp: unknown auth mode %q", c.Auth)
	}
	switch c.TLS {
	case SMTPTLSNone, SMTPTLSSTARTTLS, SMTPTLSImplicit:
	default:
		return fmt.Errorf("smtp: unknown TLS mode %q", c.TLS)
	}
	if c.Auth != SMTPAuthNone && c.Username == "" {
		return fmt.Errorf("smtp: username is required for auth mode %q", c.Auth)
	}
	return nil
}

// SMTPSender delivers notifications by email. It implements Sender.
//
// Credentials and message bodies never appear in logs: log lines carry the
// recipient address and outcome only.
type SMTPSender struct {
	cfg SMTPConfig
	log *slog.Logger
	// dial is the seam tests use to fake the transport; production uses a
	// net.Dialer with cfg.DialTimeout (+ TLS wrap for implicit mode).
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewSMTPSender builds a sender. log may be nil (slog.Default).
func NewSMTPSender(cfg SMTPConfig, log *slog.Logger) (*SMTPSender, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	s := &SMTPSender{cfg: cfg, log: log}
	s.dial = s.defaultDial
	return s, nil
}

// Channel implements Sender.
func (s *SMTPSender) Channel() domain.NotificationChannel { return domain.NotificationEmail }

// Send delivers n to n.Recipient.
func (s *SMTPSender) Send(ctx context.Context, n *domain.Notification) error {
	to := strings.TrimSpace(n.Recipient)
	if to == "" || !strings.Contains(to, "@") {
		return fmt.Errorf("smtp: invalid recipient %q", n.Recipient)
	}
	msg := buildMessage(s.cfg.From, to, n.Subject, n.Body)
	exp := backoff.NewExponentialBackOff()
	exp.InitialInterval = s.cfg.InitialInterval
	exp.MaxInterval = s.cfg.MaxInterval
	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		return struct{}{}, s.trySend(ctx, to, msg)
	}, backoff.WithBackOff(exp), backoff.WithMaxTries(uint(s.cfg.MaxTries)),
		backoff.WithNotify(func(err error, d time.Duration) {
			s.log.Warn("smtp delivery retrying", "to", to, "error", err.Error(), "backoff", d.String())
		}))
	if err != nil {
		return fmt.Errorf("smtp to %q: %w", to, err)
	}
	s.log.Info("smtp delivered", "to", to, "subject", n.Subject)
	return nil
}

// Test sends a test message to an arbitrary address without persisting it.
func (s *SMTPSender) Test(ctx context.Context, to, message string) error {
	return s.Send(ctx, &domain.Notification{Recipient: to, Subject: "Vaultix SMTP test", Body: message})
}

func (s *SMTPSender) defaultDial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: s.cfg.DialTimeout}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if s.cfg.TLS == SMTPTLSImplicit {
		tlsCfg := s.cfg.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
		}
		conn = tls.Client(conn, tlsCfg)
		if err := conn.SetDeadline(time.Now().Add(s.cfg.DialTimeout)); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if err := conn.(*tls.Conn).HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

func (s *SMTPSender) trySend(ctx context.Context, to string, msg []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	conn, err := s.dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err) // transient: retry
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(s.cfg.IOTimeout)); err != nil {
		return backoff.Permanent(fmt.Errorf("set deadline: %w", err))
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = c.Close() }()
	hello := "vaultix-manager"
	if hn := strings.SplitN(s.cfg.From, "@", 2); len(hn) == 2 {
		hello = hn[1]
	}
	if err := c.Hello(hello); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	tlsActive := s.cfg.TLS == SMTPTLSImplicit
	if _, isTLS := conn.(*tls.Conn); isTLS {
		tlsActive = true
	}
	if s.cfg.TLS == SMTPTLSSTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			tlsCfg := s.cfg.TLSConfig
			if tlsCfg == nil {
				tlsCfg = &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
			}
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
			tlsActive = true
		} else if s.cfg.Auth != SMTPAuthNone {
			return backoff.Permanent(fmt.Errorf("smtp: server offers no STARTTLS; refusing auth over cleartext"))
		}
	}
	if s.cfg.Auth != SMTPAuthNone {
		if !tlsActive && !isLocalhost(s.cfg.Host) {
			return backoff.Permanent(fmt.Errorf("smtp: refusing auth over unencrypted connection to %q", s.cfg.Host))
		}
		var auth smtp.Auth
		switch s.cfg.Auth {
		case SMTPAuthPlain:
			auth = plainAuth(s.cfg.Username, s.cfg.Password)
		case SMTPAuthLogin:
			auth = loginAuth(s.cfg.Username, s.cfg.Password)
		}
		if err := c.Auth(auth); err != nil {
			// Auth failure is almost always a bad credential: fail fast.
			return backoff.Permanent(fmt.Errorf("smtp auth: %w", err))
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return backoff.Permanent(fmt.Errorf("smtp mail from: %w", err))
	}
	if err := c.Rcpt(to); err != nil {
		return backoff.Permanent(fmt.Errorf("smtp rcpt to: %w", err))
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp commit: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func isLocalhost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func buildMessage(from, to, subject, body string) []byte {
	// Minimal RFC 5322 message. Subjects are single-lined to prevent header
	// injection; bodies are plain text.
	subject = strings.ReplaceAll(strings.ReplaceAll(subject, "\r", " "), "\n", " ")
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// plainAuth implements the SMTP PLAIN mechanism without stdlib's
// localhost/TLS gate (this sender enforces its own TLS policy above).
func plainAuth(username, password string) smtp.Auth {
	return &plainLoginAuth{username: username, password: password, login: false}
}

// loginAuth implements the SMTP LOGIN mechanism (challenge/response).
func loginAuth(username, password string) smtp.Auth {
	return &plainLoginAuth{username: username, password: password, login: true}
}

type plainLoginAuth struct {
	username string
	password string
	login    bool
	step     int
}

func (a *plainLoginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	if !a.login {
		return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
	}
	return "LOGIN", nil, nil
}

func (a *plainLoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if !a.login {
		return nil, fmt.Errorf("smtp: unexpected PLAIN challenge")
	}
	// LOGIN challenges arrive base64-encoded ("VXNlcm5hbWU6" = "Username:").
	challenge := strings.ToLower(string(fromServer))
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(fromServer))); err == nil {
		challenge = strings.ToLower(string(decoded))
	}
	var resp string
	switch {
	case strings.Contains(challenge, "username"):
		resp = a.username
		a.step = 1
	case strings.Contains(challenge, "password"):
		resp = a.password
		a.step = 2
	case a.step == 0:
		// Some servers send an empty initial challenge.
		resp = a.username
		a.step = 1
	default:
		return nil, fmt.Errorf("smtp: unexpected LOGIN challenge")
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(resp))), nil
}
