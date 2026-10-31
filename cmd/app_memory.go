package cmd

import (
	"fmt"
	"runtime"

	"github.com/dymmer-code/dym/internal/daemon"
	"github.com/spf13/cobra"
)

func newAppCommand() *cobra.Command {
	app := &cobra.Command{Use: "app", Short: "Inspect applications on this host"}
	var manager, service, path, config string
	memory := &cobra.Command{Use: "memory", Short: "Read application memory locally as versioned JSON (no Dymmer connection)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if config == "" {
			var err error
			config, err = daemon.DefaultConfigPath()
			if err != nil {
				return err
			}
		}
		result := daemon.ReadAppMemory(daemon.HostOpEnv(runtime.GOOS, config), manager, service, path)
		if result.Status != "OK" {
			return fmt.Errorf("%s", result.Message)
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		return err
	}}
	memory.Flags().StringVar(&manager, "manager", "", "systemctl, docker, docker_compose, service or init_d")
	memory.Flags().StringVar(&service, "service", "", "authorized service or container name")
	memory.Flags().StringVar(&path, "path", "", "absolute Compose project working directory")
	memory.Flags().StringVar(&config, "config", "", "observation policy (same format as daemon.yaml)")
	_ = memory.MarkFlagRequired("manager")
	_ = memory.MarkFlagRequired("service")
	app.AddCommand(memory)
	return app
}
