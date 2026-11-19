package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// meResponse mirrors GET /api/auth/me (the loginResponse shape): the user,
// their roles, and the session expiry. Scopes is forward-compatible space
// for the sibling API-keys track (omitted by the server today).
type meResponse struct {
	User struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	} `json:"user"`
	Roles []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"roles"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (a *app) newLoginCommand() *cobra.Command {
	var managerFlag, keyFlag string
	cmd := &cobra.Command{
		Use:   "login --manager URL --api-key KEY",
		Short: "Store Manager credentials (verifies with GET /api/auth/me)",
		Long: `Stores the Manager URL and API key in the config file (0600).

Values resolve as --flag, then VAULTIX_MANAGER_URL / VAULTIX_API_KEY, then the
existing config file; when stdin is a terminal, missing values are prompted
(the key is never echoed back). Credentials are verified against
GET /api/auth/me before anything is stored.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := a.configPath()
			existing, err := loadFileConfig(path)
			if err != nil {
				return err
			}
			manager := firstNonEmpty(managerFlag, os.Getenv(envManagerURL), existing.ManagerURL)
			if manager == "" {
				manager, err = promptIfTTY(cmd, "Manager URL: ")
				if err != nil {
					return fmt.Errorf("missing manager URL (use --manager or set VAULTIX_MANAGER_URL)")
				}
			}
			key := firstNonEmpty(keyFlag, os.Getenv(envAPIKey), existing.APIKey)
			if key == "" {
				key, err = promptIfTTY(cmd, "API key: ")
				if err != nil {
					return fmt.Errorf("missing API key (use --api-key or set VAULTIX_API_KEY)")
				}
			}
			client := NewClient(manager, key, a.debugFlag, cmd.ErrOrStderr())
			raw, err := client.Do(cmd.Context(), mGet, endpoints["auth.me"].path, nil, nil)
			if err != nil {
				return err
			}
			var me meResponse
			if err := decode(raw, "auth.me", &me); err != nil {
				return err
			}
			if err := saveFileConfig(path, fileConfig{ManagerURL: manager, APIKey: key}); err != nil {
				return err
			}
			// Never print the key: only the verified identity and the path.
			cmd.Printf("Logged in as %s (%s)\n", displayName(&me), me.User.Email)
			cmd.Printf("Config saved to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&managerFlag, "manager", "", "Manager base URL, e.g. https://manager.example:8443")
	cmd.Flags().StringVar(&keyFlag, "api-key", "", "user API key (vxk_...); prefer VAULTIX_API_KEY to keep it out of shell history")
	return cmd
}

func (a *app) newLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored API key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := a.configPath()
			fc, err := loadFileConfig(path)
			if err != nil {
				return err
			}
			if strings.TrimSpace(fc.APIKey) == "" {
				cmd.Printf("Not logged in (no API key in %s)\n", path)
				return nil
			}
			fc.APIKey = ""
			if err := saveFileConfig(path, fc); err != nil {
				return err
			}
			cmd.Printf("Logged out (removed API key from %s)\n", path)
			if strings.TrimSpace(os.Getenv(envAPIKey)) != "" {
				cmd.Printf("Note: %s is still set in the environment\n", envAPIKey)
			}
			return nil
		},
	}
}

func (a *app) newWhoamiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print the current user, roles, and scopes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := a.call(cmd, "auth.me", nil, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var me meResponse
				if err := decode(raw, "auth.me", &me); err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "User:  %s (%s)\n", displayName(&me), me.User.Email)
				roles := make([]string, 0, len(me.Roles))
				for _, r := range me.Roles {
					roles = append(roles, r.Name)
				}
				fmt.Fprintf(out, "Roles: %s\n", fmtList(roles))
				if len(me.Scopes) > 0 {
					fmt.Fprintf(out, "Scopes: %s\n", strings.Join(me.Scopes, ","))
				}
				return nil
			})
		},
	}
}

func displayName(me *meResponse) string {
	if me.User.DisplayName != "" {
		return me.User.DisplayName
	}
	if me.User.Username != "" {
		return me.User.Username
	}
	return me.User.ID
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// promptIfTTY prints label to stderr and reads one line from the command's
// stdin, but only when stdin is a terminal; otherwise it returns errNoTTY so
// the caller can fail with an actionable message instead of hanging. The
// entered value is never echoed.
func promptIfTTY(cmd *cobra.Command, label string) (string, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); !ok || !isCharDevice(f) {
		return "", errNoTTY
	}
	fmt.Fprint(cmd.ErrOrStderr(), label)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", errNoTTY
	}
	return strings.TrimSpace(line), nil
}

// errNoTTY signals "stdin is not a terminal" to prompt callers.
var errNoTTY = fmt.Errorf("stdin is not a terminal")

func isCharDevice(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// readConfirmLine reads one confirmation line from stdin (piped input is
// fine here: only secret prompts require a TTY).
func readConfirmLine(cmd *cobra.Command) (string, error) {
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", io.EOF
	}
	return strings.TrimSpace(line), nil
}
