package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// API-key DTOs target the user-API-keys slice (/api/api-keys CRUD):
// shapes below match the server exactly (ttl_hours float, revocation as
// revoked_at), decoded leniently (unknown fields ignored, both
// {items:[...]} and bare [...] list shapes accepted) so small server
// drifts reconcile in the endpoint table and these structs rather than in
// command logic.
type apiKeyJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	KeyPrefix  string     `json:"key_prefix"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  *time.Time `json:"created_at"`
}

type apiKeyCreateRequest struct {
	Name     string   `json:"name"`
	Scopes   []string `json:"scopes,omitempty"`
	TTLHours float64  `json:"ttl_hours,omitempty"`
}

// secretFieldNames are the candidate JSON fields carrying the shown-once key
// in the create response, in lookup order.
var secretFieldNames = []string{"api_key", "key", "token", "secret"}

func (a *app) newAPIKeysCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apikeys",
		Short: "Manage user API keys",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List API keys (prefixes only; full keys are never re-shown)",
			RunE: func(cmd *cobra.Command, _ []string) error {
				raw, err := a.call(cmd, "apikeys.list", nil, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					keys, err := decodeAPIKeyList(raw)
					if err != nil {
						return err
					}
					if len(keys) == 0 {
						cmd.Println("No API keys found.")
						return nil
					}
					rows := make([][]string, 0, len(keys))
					for _, k := range keys {
						rows = append(rows, []string{
							k.ID, k.Name, fmtList(k.Scopes), orDash(k.KeyPrefix),
							fmtTime(k.ExpiresAt), fmtBool(k.RevokedAt != nil),
						})
					}
					writeTable(cmd.OutOrStdout(),
						[]string{"ID", "NAME", "SCOPES", "PREFIX", "EXPIRES", "REVOKED"}, rows)
					return nil
				})
			},
		},
		a.newAPIKeysCreateCommand(),
		&cobra.Command{
			Use:   "revoke <id>",
			Short: "Revoke an API key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := a.call(cmd, "apikeys.revoke", map[string]string{"id": args[0]}, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					cmd.Printf("Revoked API key %s\n", args[0])
					return nil
				})
			},
		},
	)
	return cmd
}

func (a *app) newAPIKeysCreateCommand() *cobra.Command {
	var name, scopes, ttl string
	cmd := &cobra.Command{
		Use:   "create --name NAME [--scopes S1,S2] [--ttl TTL]",
		Short: "Create an API key (prints the key ONCE)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("missing required flag --name")
			}
			var ttlHours float64
			if t := strings.TrimSpace(ttl); t != "" {
				d, err := time.ParseDuration(t)
				if err != nil {
					return fmt.Errorf("invalid --ttl %q: want a Go duration like 720h (server bounds 1m..8760h)", t)
				}
				ttlHours = d.Hours()
			}
			body := apiKeyCreateRequest{
				Name:     name,
				Scopes:   splitCSV(scopes),
				TTLHours: ttlHours,
			}
			raw, err := a.call(cmd, "apikeys.create", nil, nil, body)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				secret, meta := extractSecret(raw)
				if secret == "" {
					return fmt.Errorf("server response did not include the new API key (looked for %s)",
						strings.Join(secretFieldNames, "/"))
				}
				out := cmd.OutOrStdout()
				fmt.Fprintln(out, "WARNING: this API key is shown ONCE. Save it now — it cannot be retrieved later.")
				fmt.Fprintln(out)
				fmt.Fprintf(out, "  %s\n", secret)
				fmt.Fprintln(out)
				if id, _ := meta["id"].(string); id != "" {
					fmt.Fprintf(out, "ID: %s\n", id)
				}
				fmt.Fprintf(out, "Name: %s\n", name)
				if len(body.Scopes) > 0 {
					fmt.Fprintf(out, "Scopes: %s\n", strings.Join(body.Scopes, ","))
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "key name (required)")
	cmd.Flags().StringVar(&scopes, "scopes", "", "comma-separated scopes")
	cmd.Flags().StringVar(&ttl, "ttl", "", "time-to-live, e.g. 720h (server default when empty)")
	return cmd
}

// decodeAPIKeyList accepts both {"items":[...]} and bare [...] list shapes.
func decodeAPIKeyList(raw json.RawMessage) ([]apiKeyJSON, error) {
	var env struct {
		Items []apiKeyJSON `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Items != nil {
		return env.Items, nil
	}
	var bare []apiKeyJSON
	if err := json.Unmarshal(raw, &bare); err != nil {
		return nil, fmt.Errorf("decode apikeys.list response: %w", err)
	}
	return bare, nil
}

// extractSecret finds the shown-once key in a create response, looking at
// the top level and one "data" nesting level; it also returns the metadata
// object for the ID line.
func extractSecret(raw json.RawMessage) (string, map[string]any) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return "", nil
	}
	if s := firstSecretField(top); s != "" {
		return s, top
	}
	if nested, ok := top["data"].(map[string]any); ok {
		if s := firstSecretField(nested); s != "" {
			return s, nested
		}
	}
	return "", top
}

func firstSecretField(obj map[string]any) string {
	for _, name := range secretFieldNames {
		if s, ok := obj[name].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
