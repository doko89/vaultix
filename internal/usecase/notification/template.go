package notification

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/vaultix/vaultix/internal/domain"
)

const (
	maxRuleHeaders       = 16
	maxBodyTemplateBytes = 8 << 10
	maxRenderedBodyBytes = 256 << 10
)

// TemplateData exposes notification fields to body templates as
// {{.event}}, {{.subject}}, {{.body}}, {{.recipient}}, {{.ref_type}},
// {{.ref_id}}, {{.attempt}}, {{.sent_at}}.
type TemplateData struct {
	Event     string
	Subject   string
	Body      string
	Recipient string
	RefType   string
	RefID     string
	Attempt   int
	SentAt    string
}

// CheckBodyTemplate parses the template and trial-renders it so unknown
// variables fail at save time, not at 3am delivery.
func CheckBodyTemplate(tmpl string) error {
	t, err := template.New("body").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("%w: invalid body template: %v", domain.ErrValidation, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, TemplateData{}.vars()); err != nil {
		return fmt.Errorf("%w: invalid body template: %v", domain.ErrValidation, err)
	}
	return nil
}

// RenderBodyTemplate executes a validated template over data.
func RenderBodyTemplate(tmpl string, data TemplateData) (string, error) {
	t, err := template.New("body").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("notification: invalid body template: %v", err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data.vars()); err != nil {
		return "", fmt.Errorf("notification: render body template: %v", err)
	}
	if b.Len() > maxRenderedBodyBytes {
		return "", fmt.Errorf("notification: rendered body exceeds %d bytes", maxRenderedBodyBytes)
	}
	return b.String(), nil
}

// vars exposes the data under lowercase keys: Go templates address struct
// fields case-sensitively ({{.Event}}), but users write {{.event}}.
func (d TemplateData) vars() map[string]any {
	return map[string]any{
		"event":     d.Event,
		"subject":   d.Subject,
		"body":      d.Body,
		"recipient": d.Recipient,
		"ref_type":  d.RefType,
		"ref_id":    d.RefID,
		"attempt":   d.Attempt,
		"sent_at":   d.SentAt,
	}
}
