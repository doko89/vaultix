package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// app carries the persistent flag values for one CLI invocation.
type app struct {
	configFlag string
	outputFlag string
	debugFlag  bool
}

func newRootCommand() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:   "vaultix-cli",
		Short: "Vaultix Manager command-line client",
		Long: `vaultix-cli talks to the Vaultix Manager API with a user API key.

Credentials live in ~/.config/vaultix/cli.yaml (0600). Run 'vaultix-cli login'
once, or export VAULTIX_MANAGER_URL and VAULTIX_API_KEY.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.configFlag, "config", "", "config file path (default "+defaultConfigPath()+")")
	root.PersistentFlags().StringVar(&a.outputFlag, "output", formatTable, "output format: table|json")
	root.PersistentFlags().BoolVar(&a.debugFlag, "debug", false, "log method+URL+status per request (never secrets)")

	root.AddCommand(
		a.newLoginCommand(),
		a.newLogoutCommand(),
		a.newWhoamiCommand(),
		a.newAgentsCommand(),
		a.newPoliciesCommand(),
		a.newJobsCommand(),
		a.newCatalogCommand(),
		a.newBackupCommand(),
		a.newRestoreCommand(),
		a.newAPIKeysCommand(),
		a.newUpdatesCommand(),
		newVersionCommand(),
	)
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print vaultix-cli version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("vaultix-cli %s\n", version)
		},
	}
}

// outputFormat validates and returns the requested --output value.
func (a *app) outputFormat() (string, error) {
	switch a.outputFlag {
	case formatTable, formatJSON, "":
		if a.outputFlag == "" {
			return formatTable, nil
		}
		return a.outputFlag, nil
	default:
		return "", fmt.Errorf("invalid --output %q: want table|json", a.outputFlag)
	}
}

// configPath returns the effective config file path.
func (a *app) configPath() string {
	return resolveConfigPath(a.configFlag)
}

// resolve loads the config file and applies env overrides.
func (a *app) resolve() (*resolvedConfig, error) {
	return resolveConfig(a.configPath())
}

// mustClient resolves config, requires both manager URL and API key, and
// builds the Bearer-authed client. Every data command funnels through here
// so "not logged in" always yields the same actionable error.
func (a *app) mustClient(cmd *cobra.Command) (*Client, error) {
	rc, err := a.resolve()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rc.ManagerURL) == "" {
		return nil, fmt.Errorf("no manager URL configured (run `vaultix-cli login --manager URL` or set VAULTIX_MANAGER_URL)")
	}
	if strings.TrimSpace(rc.APIKey) == "" {
		return nil, fmt.Errorf("not logged in: no API key configured (run `vaultix-cli login` or set VAULTIX_API_KEY)")
	}
	return NewClient(rc.ManagerURL, rc.APIKey, a.debugFlag, cmd.ErrOrStderr()), nil
}

// call expands the named endpoint from the table and performs it.
func (a *app) call(cmd *cobra.Command, name string, pathArgs map[string]string, query url.Values, body any) (json.RawMessage, error) {
	client, err := a.mustClient(cmd)
	if err != nil {
		return nil, err
	}
	method, path, err := expandEndpoint(name, pathArgs)
	if err != nil {
		return nil, err
	}
	return client.Do(cmd.Context(), method, path, query, body)
}

// emit prints raw as JSON (--output json) or runs printTable for --output
// table. Every data command uses it so both formats stay consistent.
func (a *app) emit(cmd *cobra.Command, raw json.RawMessage, printTable func() error) error {
	format, err := a.outputFormat()
	if err != nil {
		return err
	}
	if format == formatJSON {
		return writeJSON(cmd.OutOrStdout(), raw)
	}
	return printTable()
}

// decode unmarshals a Manager response into v with the endpoint named for
// actionable errors when the server shape drifts.
func decode(raw json.RawMessage, endpointName string, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decode %s response: %w", endpointName, err)
	}
	return nil
}
