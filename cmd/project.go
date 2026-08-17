package cmd

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

// newProjectCommand builds the "dym project <project> secrets get" entry
// point. Like newDomainCommand, cobra cannot natively route a dynamic
// positional token (the project name) in the middle of a command path, so
// this command disables its own flag parsing, takes every remaining
// argument itself, peels off the project name, and executes a small nested
// command tree (secrets) against the rest — closing over the project name
// and deps.
func newProjectCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "project <project> secrets get",
		Short:              "Manage secrets for a project",
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := args[0]
			nested := newProjectResourceTree(project, deps)
			nested.SetOut(cmd.OutOrStdout())
			nested.SetErr(cmd.ErrOrStderr())
			nested.SetArgs(args[1:])
			return nested.Execute()
		},
	}
	return cmd
}

// newProjectResourceTree builds the secrets command tree for a single,
// already-known project name. Its own error/usage printing is silenced so
// the single outer "dym" root command prints any resulting error exactly
// once.
func newProjectResourceTree(project string, deps Dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:               "project-resources",
		Annotations:       map[string]string{cobra.CommandDisplayNameAnnotation: "dym project " + project},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		SilenceUsage:      true,
		SilenceErrors:     true,
	}
	root.AddCommand(newSecretsCommand(project, deps))
	return root
}

func newSecretsCommand(project string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "secrets", Short: "Manage project secrets"}
	cmd.AddCommand(newSecretsGetCommand(project, deps))
	return cmd
}

func newSecretsGetCommand(project string, deps Dependencies) *cobra.Command {
	var env, deployment, output string
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch secrets for a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var wireFormat string
			switch output {
			case "table", "json":
				wireFormat = ""
			case "dotenv":
				wireFormat = ".env"
			default:
				return errors.New(`--output must be "table", "json", or "dotenv"`)
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			result, err := client.GetSecrets(cmd.Context(), project, env, deployment, wireFormat)
			if err != nil {
				return wrapAuthError(err)
			}
			if wireFormat == ".env" {
				_, err := io.WriteString(cmd.OutOrStdout(), result.Raw)
				return err
			}
			if output == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result.Entries)
			}
			return writeSecretsTable(cmd.OutOrStdout(), result.Entries)
		},
	}
	cmd.Flags().StringVar(&env, "env", "dev", "Environment to fetch secrets for")
	cmd.Flags().StringVar(&deployment, "deployment", "", "Deployment name (omit for the default deployment)")
	cmd.Flags().StringVarP(&output, "output", "o", "table", `Output format: "table", "json", or "dotenv"`)
	return cmd
}
