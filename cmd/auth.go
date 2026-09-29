package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dymmer-code/dym/internal/api"
	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/spf13/cobra"
)

const tokenHelp = "Sign in at https://dymmer.com/keys, select Generate or Regenerate, then copy the token immediately. Dymmer will not show it again; regenerating revokes the prior token."

func newAuthCommand(deps Dependencies) *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Manage authentication"}
	auth.AddCommand(newAuthLoginCommand(deps))
	auth.AddCommand(newAuthLogoutCommand(deps))
	auth.AddCommand(newAuthStatusCommand(deps))
	auth.AddCommand(newAuthTokenHelpCommand())
	return auth
}

func newAuthTokenHelpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "token-help",
		Short: "Explain how to get an API token",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), tokenHelp)
		},
	}
}

func newAuthLoginCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Save a Dymmer API token securely",
		Long:  "Save a Dymmer API token securely.\n\n" + tokenHelp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := deps.ReadSecret("Paste your Dymmer API token: ")
			if err != nil {
				return fmt.Errorf("unable to read token: %w", err)
			}
			token := strings.TrimSpace(raw)
			if token == "" {
				return errors.New("no token provided")
			}
			if err := deps.Store.Set(token); err != nil {
				return fmt.Errorf("unable to save token to the system credential store: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "API token saved securely.")
			return nil
		},
	}
}

func newAuthLogoutCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved Dymmer API token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := deps.Store.Delete(); err != nil {
				return fmt.Errorf("unable to remove token from the system credential store: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "API token removed.")
			return nil
		},
	}
}

func newAuthStatusCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which credential source will be used and its scopes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, source, err := credentials.Resolve(deps.Env, deps.Store)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No credentials available. Run \"dym auth login\" or set DYMMER_TOKEN.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated via %s.\n", source)

			client, err := resolveAPI(deps)
			if err != nil {
				return nil
			}
			status, err := client.GetAuthStatus(cmd.Context())
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Could not verify token scope: %v\n", wrapAuthError(err))
				return nil
			}
			printAuthStatus(cmd, status)
			return nil
		},
	}
}

func printAuthStatus(cmd *cobra.Command, status *api.AuthStatus) {
	switch {
	case status.Unrestricted:
		fmt.Fprintln(cmd.OutOrStdout(), "Scope: full access (unrestricted).")
	case len(status.Scopes) == 0:
		fmt.Fprintln(cmd.OutOrStdout(), "Scope: none -- this token can't do anything.")
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "Scopes: %s\n", strings.Join(status.Scopes, ", "))
	}

	keys := make([]string, 0, len(status.Constraints))
	for k := range status.Constraints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(cmd.OutOrStdout(), "Restricted %s: %s\n", k, status.Constraints[k])
	}
}
