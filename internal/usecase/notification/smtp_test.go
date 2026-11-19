package notification

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

// fakeSMTP is a scriptable in-memory SMTP server speaking over net.Pipe. It
// records delivered messages and completed AUTH mechanisms.
type fakeSMTP struct {
	mu       sync.Mutex
	messages []string
	authSeen []string
	// failData makes the DATA commit fail (transient-style 4xx).
	failData bool
	// advertiseAuth controls the AUTH line in EHLO (empty = no AUTH line).
	advertiseAuth string
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) {
		_, _ = w.WriteString(s + "\r\n")
		_ = w.Flush()
	}
	write("220 fake ESMTP ready")
	var data strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				if f.failData {
					write("451 temporary failure")
				} else {
					f.mu.Lock()
					f.messages = append(f.messages, data.String())
					f.mu.Unlock()
					write("250 ok")
				}
				data.Reset()
				continue
			}
			data.WriteString(line + "\n")
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			write("250-fake greets")
			if f.advertiseAuth != "" {
				write("250-AUTH " + f.advertiseAuth)
			}
			write("250 8BITMIME")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			f.mu.Lock()
			f.authSeen = append(f.authSeen, "PLAIN")
			f.mu.Unlock()
			write("235 authenticated")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			f.mu.Lock()
			f.authSeen = append(f.authSeen, "LOGIN")
			f.mu.Unlock()
			write("334 VXNlcm5hbWU6") // "Username:"
			u, err := r.ReadString('\n')
			if err != nil {
				return
			}
			_ = u
			write("334 UGFzc3dvcmQ6") // "Password:"
			p, err := r.ReadString('\n')
			if err != nil {
				return
			}
			_ = p
			write("235 authenticated")
		case strings.HasPrefix(upper, "MAIL FROM"):
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			write("250 ok")
		case upper == "DATA":
			write("354 end with .")
			inData = true
		case upper == "QUIT":
			write("221 bye")
			return
		case upper == "RSET":
			write("250 ok")
		default:
			write("502 unsupported: " + line)
		}
	}
}

func smtpTestSender(t *testing.T, fake *fakeSMTP, mutate func(*SMTPConfig)) *SMTPSender {
	t.Helper()
	cfg := SMTPConfig{
		Host: "localhost", Port: 2525, From: "vaultix@example.com",
		Auth: SMTPAuthNone, TLS: SMTPTLSNone,
		DialTimeout: time.Second, IOTimeout: 5 * time.Second,
		MaxTries: 1, InitialInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	sender, err := NewSMTPSender(cfg, discardLogger())
	require.NoError(t, err)
	sender.dial = func(_ context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go fake.serve(server)
		return client, nil
	}
	return sender
}

func TestSMTPSendSuccess(t *testing.T) {
	fake := &fakeSMTP{}
	sender := smtpTestSender(t, fake, nil)
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "job failed", Body: "job j-1 failed",
	})
	require.NoError(t, err)
	require.Len(t, fake.messages, 1)
	assert.Contains(t, fake.messages[0], "To: ops@example.com")
	assert.Contains(t, fake.messages[0], "Subject: job failed")
	assert.Contains(t, fake.messages[0], "job j-1 failed")
	assert.Empty(t, fake.authSeen)
}

func TestSMTPPlainAuthLocalhost(t *testing.T) {
	fake := &fakeSMTP{advertiseAuth: "PLAIN LOGIN"}
	sender := smtpTestSender(t, fake, func(c *SMTPConfig) {
		c.Auth = SMTPAuthPlain
		c.Username = "user"
		c.Password = "pass"
	})
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "s", Body: "b",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"PLAIN"}, fake.authSeen)
}

func TestSMTPLoginAuthFlow(t *testing.T) {
	fake := &fakeSMTP{advertiseAuth: "LOGIN"}
	sender := smtpTestSender(t, fake, func(c *SMTPConfig) {
		c.Auth = SMTPAuthLogin
		c.Username = "user"
		c.Password = "pass"
	})
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "s", Body: "b",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"LOGIN"}, fake.authSeen)
	require.Len(t, fake.messages, 1)
}

func TestSMTPRefusesAuthOverCleartextToRemote(t *testing.T) {
	fake := &fakeSMTP{}
	sender := smtpTestSender(t, fake, func(c *SMTPConfig) {
		c.Host = "smtp.example.com" // not local
		c.Auth = SMTPAuthPlain
		c.Username = "user"
		c.Password = "pass"
		c.TLS = SMTPTLSNone
	})
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "s", Body: "b",
	})
	require.ErrorContains(t, err, "refusing auth over unencrypted connection")
	assert.Empty(t, fake.authSeen, "no credentials may be offered over cleartext")
	assert.Empty(t, fake.messages)
}

func TestSMTPRequiresSTARTTLSForAuth(t *testing.T) {
	fake := &fakeSMTP{} // advertises no STARTTLS
	sender := smtpTestSender(t, fake, func(c *SMTPConfig) {
		c.Host = "smtp.example.com"
		c.Auth = SMTPAuthPlain
		c.Username = "user"
		c.Password = "pass"
		c.TLS = SMTPTLSSTARTTLS
	})
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "s", Body: "b",
	})
	require.ErrorContains(t, err, "no STARTTLS")
}

func TestSMTPRetriesTransientThenSucceeds(t *testing.T) {
	fake := &fakeSMTP{}
	var dials atomic.Int32
	cfg := SMTPConfig{
		Host: "localhost", Port: 2525, From: "vaultix@example.com",
		Auth: SMTPAuthNone, TLS: SMTPTLSNone,
		DialTimeout: time.Second, IOTimeout: 5 * time.Second,
		MaxTries: 3, InitialInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond,
	}
	sender, err := NewSMTPSender(cfg, discardLogger())
	require.NoError(t, err)
	sender.dial = func(_ context.Context, _, _ string) (net.Conn, error) {
		if dials.Add(1) < 3 {
			return nil, fmt.Errorf("connection refused")
		}
		client, server := net.Pipe()
		go fake.serve(server)
		return client, nil
	}
	err = sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "s", Body: "b",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), dials.Load())
	require.Len(t, fake.messages, 1)
}

func TestSMTPGivesUpAfterBudget(t *testing.T) {
	var dials atomic.Int32
	sender := smtpTestSender(t, &fakeSMTP{}, func(c *SMTPConfig) { c.MaxTries = 2 })
	sender.dial = func(_ context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("connection refused")
	}
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "s", Body: "b",
	})
	require.Error(t, err)
	assert.Equal(t, int32(2), dials.Load(), "must stop after the retry budget")
}

func TestSMTPHeaderInjectionBlocked(t *testing.T) {
	fake := &fakeSMTP{}
	sender := smtpTestSender(t, fake, nil)
	err := sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops@example.com", Subject: "hi\r\nBcc: evil@example.com", Body: "b",
	})
	require.NoError(t, err)
	require.Len(t, fake.messages, 1)
	// The payload text may survive inside the Subject line, but it must not
	// become its own header line.
	for _, line := range strings.Split(fake.messages[0], "\n") {
		assert.NotEqual(t, "Bcc: evil@example.com", strings.TrimSpace(line))
	}
}

func TestNewSMTPSenderValidation(t *testing.T) {
	_, err := NewSMTPSender(SMTPConfig{From: "a@example.com"}, discardLogger())
	assert.Error(t, err) // missing host
	_, err = NewSMTPSender(SMTPConfig{Host: "localhost"}, discardLogger())
	assert.Error(t, err) // missing from
	_, err = NewSMTPSender(SMTPConfig{Host: "localhost", From: "a@example.com", Auth: "oauth"}, discardLogger())
	assert.Error(t, err) // bad auth
	_, err = NewSMTPSender(SMTPConfig{Host: "localhost", From: "a@example.com", TLS: "super"}, discardLogger())
	assert.Error(t, err) // bad TLS
}
