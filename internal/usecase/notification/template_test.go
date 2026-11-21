package notification

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckBodyTemplate(t *testing.T) {
	require.NoError(t, CheckBodyTemplate(`{"text":"{{.event}} {{.subject}}"}`))
	require.NoError(t, CheckBodyTemplate(`plain text without variables`))
	require.Error(t, CheckBodyTemplate(`{"text":"{{.event}"}`))
	require.Error(t, CheckBodyTemplate(`{"text":"{{.bogus}}}"}`))
}

func TestRenderBodyTemplate(t *testing.T) {
	out, err := RenderBodyTemplate(
		`{{.event}}|{{.subject}}|{{.body}}|{{.recipient}}|{{.ref_type}}|{{.ref_id}}|{{.attempt}}|{{.sent_at}}`,
		TemplateData{Event: "job_failed", Subject: "s", Body: "b", Recipient: "r",
			RefType: "backup_job", RefID: "j-1", Attempt: 2, SentAt: "2026-01-01T00:00:00Z"},
	)
	require.NoError(t, err)
	assert.Equal(t, "job_failed|s|b|r|backup_job|j-1|2|2026-01-01T00:00:00Z", out)
}

func TestRuleValidateHeadersTemplate(t *testing.T) {
	base := Rule{Name: "n", Events: []string{EventJobFailed}, Channel: "webhook", Recipient: "https://x", Enabled: true}
	ok := base
	ok.Headers = map[string]string{"Authorization": "Bearer x"}
	ok.BodyTemplate = `{"text":"{{.subject}}"}`
	require.NoError(t, ok.Validate())

	bad := base
	bad.Headers = map[string]string{"": "v"}
	require.Error(t, bad.Validate())

	bad = base
	bad.Headers = map[string]string{"X-A": "a\nb"}
	require.Error(t, bad.Validate())

	many := base
	many.Headers = map[string]string{}
	for i := range maxRuleHeaders + 1 {
		many.Headers["X-Header-"+string(rune('A'+i/10))+string(rune('0'+i%10))] = "v"
	}
	require.Len(t, many.Headers, maxRuleHeaders+1)
	require.Error(t, many.Validate())

	bad = base
	bad.BodyTemplate = `{{.bogus}}`
	require.Error(t, bad.Validate())

	bad = base
	bad.BodyTemplate = strings.Repeat("x", maxBodyTemplateBytes+1)
	require.Error(t, bad.Validate())
}
