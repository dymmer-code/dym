package cmd

import (
	"fmt"
	"github.com/dymmer-code/dym/internal/builderregistration"
	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/dymmer-code/dym/internal/daemon"
	"github.com/spf13/cobra"
	"strings"
)

func newHostCommand(deps Dependencies) *cobra.Command {
	host := &cobra.Command{Use: "host", Short: "Manage this paired host"}
	builder := &cobra.Command{Use: "builder", Short: "Register a locally installed dymploy"}
	var flags daemonFlags
	var config, name, apiURL, tokenOwner, caFile string
	register := &cobra.Command{Use: "register", Short: "Register a builder and save its profile credential", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		socket, statePath, err := flags.resolve(deps)
		if err != nil {
			return err
		}
		state, err := daemon.LoadState(statePath)
		if err != nil {
			return err
		}
		token, _, err := credentials.Resolve(deps.Env, deps.Store)
		if err != nil {
			return err
		}
		server := strings.TrimSuffix(socket, "/daemon/websocket")
		server = strings.Replace(server, "wss://", "https://", 1)
		server = strings.Replace(server, "ws://", "http://", 1)
		result, err := builderregistration.Register(cmd.Context(), builderregistration.Options{Config: config, Server: server, HostID: state.HostID, HostSecret: state.Secret, Token: token, Name: name, APIURL: apiURL, TokenOwner: tokenOwner, CAFile: caFile})
		if err != nil {
			return err
		}
		fmt.Fprintf(deps.Out, "Builder %s registered. Credential saved; reload dymploy to connect.\n", result.BuilderID)
		return nil
	}}
	flags.addPairing(register)
	register.Flags().StringVar(&config, "config", "", "path to the standalone dymploy YAML configuration")
	register.Flags().StringVar(&name, "name", "dymploy", "builder profile name on this host")
	register.Flags().StringVar(&apiURL, "api-url", "", "builder API URL (defaults to its configured listen address)")
	register.Flags().StringVar(&tokenOwner, "token-owner", "", "service account that must read the secret (default: configuration owner)")
	register.Flags().StringVar(&caFile, "ca-file", "", "additional trusted CA certificate for a private Dymmer instance")
	register.MarkFlagRequired("config")
	builder.AddCommand(register)
	host.AddCommand(builder)
	return host
}
