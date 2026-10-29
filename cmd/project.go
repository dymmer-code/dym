package cmd

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

// newProjectCommand builds the "dym project <project>" entry
// point. Like newDomainCommand, cobra cannot natively route a dynamic
// positional token (the project name) in the middle of a command path, so
// this command disables its own flag parsing, takes every remaining
// argument itself, peels off the project name, and executes a small nested
// command tree (apps and secrets) against the rest — closing over the project name
// and deps.
func newProjectCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "project <project> <apps|secrets>",
		Short:              "Manage apps and secrets for a project",
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

// newProjectResourceTree builds the apps and secrets command tree for a single,
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
	root.AddCommand(newSecretsCommand(project, deps), newProjectAppsCommand(project, deps))
	return root
}

func newSecretsCommand(project string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "secrets", Short: "Manage project secrets"}
	cmd.AddCommand(newSecretsGetCommand(project, deps))
	return cmd
}

func newSecretsGetCommand(project string, deps Dependencies) *cobra.Command {
	var env, deployment, output, selectFields string
	var filterArgs []string
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch secrets for a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var wireFormat string
			switch output {
			case "table", "json", "csv", "tsv":
				wireFormat = ""
			case "dotenv":
				wireFormat = ".env"
			default:
				return errors.New(`--output must be "table", "json", "csv", "tsv", or "dotenv"`)
			}
			if wireFormat == ".env" && (len(filterArgs) > 0 || selectFields != "") {
				return errors.New("--filter/--select cannot be combined with --output dotenv")
			}
			filters, err := parseFilters(filterArgs)
			if err != nil {
				return err
			}
			fields := parseSelect(selectFields)
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
			entries, rows, err := applyFilters(result.Entries, filters)
			if err != nil {
				return err
			}
			if output == "csv" || output == "tsv" {
				comma := ','
				if output == "tsv" {
					comma = '\t'
				}
				if len(fields) == 0 {
					return writeSecretsCSV(cmd.OutOrStdout(), entries, comma)
				}
				return writeSelectedDelimited(cmd.OutOrStdout(), rows, fields, comma)
			}
			if len(fields) == 0 {
				if output == "json" {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(entries)
				}
				return writeSecretsTable(cmd.OutOrStdout(), entries)
			}
			if output == "json" {
				return writeSelectedJSON(cmd.OutOrStdout(), rows, fields, true)
			}
			return writeSelectedTable(cmd.OutOrStdout(), rows, fields, "No secrets found.")
		},
	}
	cmd.Flags().StringVar(&env, "env", "dev", "Environment to fetch secrets for")
	cmd.Flags().StringVar(&deployment, "deployment", "", "Deployment name (omit for the default deployment)")
	cmd.Flags().StringVarP(&output, "output", "o", "table", `Output format: "table", "json", "csv", "tsv", or "dotenv"`)
	addFilterAndSelectFlags(cmd, &filterArgs, &selectFields)
	return cmd
}

func newProjectAppsCommand(project string, deps Dependencies) *cobra.Command {
	var env, deployment, output string
	apps := &cobra.Command{Use: "apps", Short: "List apps and their hosts"}
	list := &cobra.Command{
		Use: "list", Short: "List apps and destinations in a project environment", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case "table", "json", "csv", "tsv":
			default:
				return errors.New(`--output must be "table", "json", "csv", or "tsv"`)
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			deployments, err := client.ListDeployments(cmd.Context(), project, env, deployment)
			if err != nil {
				return wrapAuthError(err)
			}
			if output == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(deployments)
			}
			rows := []map[string]any{}
			for _, app := range deployments {
				if len(app.Hosts) == 0 {
					rows = append(rows, map[string]any{"app": app.Name, "host": "", "status": "draft", "ports": ""})
				}
				for _, host := range app.Hosts {
					rows = append(rows, map[string]any{"app": app.Name, "host": host.Name, "status": host.Status, "ports": host.Ports})
				}
			}
			fields := []string{"app", "host", "status", "ports"}
			if output == "csv" || output == "tsv" {
				comma := ','
				if output == "tsv" {
					comma = '\t'
				}
				return writeSelectedDelimited(cmd.OutOrStdout(), rows, fields, comma)
			}
			return writeSelectedTable(cmd.OutOrStdout(), rows, fields, "No apps found.")
		},
	}
	list.Flags().StringVar(&env, "env", "dev", "Project environment")
	list.Flags().StringVar(&deployment, "deployment", "", "Filter by app name")
	list.Flags().StringVarP(&output, "output", "o", "table", "Output format: table, json, csv, or tsv")
	apps.AddCommand(list)
	return apps
}
