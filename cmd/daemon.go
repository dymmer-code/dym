package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/dymmer-code/dym/internal/daemon"
	"github.com/spf13/cobra"
)

const defaultServer = "https://dymmer.com"

// PairFunc pairs this host with Dymmer; tests replace it.
type PairFunc func(ctx context.Context, o daemon.PairOptions) (daemon.State, error)

// RunDaemonFunc keeps this host connected to Dymmer until ctx ends; tests replace it.
type RunDaemonFunc func(ctx context.Context, o daemon.RunOptions) error

type daemonFlags struct {
	server string
	state  string
}

func (f *daemonFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.server, "server", "", "address of Dymmer (default $DYM_SERVER or "+defaultServer+")")
	cmd.Flags().StringVar(&f.state, "state", "", "file where the pairing is kept (default /var/lib/dym/daemon.json for root, the user's config otherwise)")
}

func (f *daemonFlags) resolve(deps Dependencies) (socket, statePath string, err error) {
	server := f.server
	if server == "" {
		server = deps.Env("DYM_SERVER")
	}
	if server == "" {
		server = defaultServer
	}
	if socket, err = daemon.SocketURL(server); err != nil {
		return "", "", err
	}

	statePath = f.state
	if statePath == "" {
		if statePath, err = daemon.DefaultStatePath(); err != nil {
			return "", "", err
		}
	}
	return socket, statePath, nil
}

func (d Dependencies) pair() PairFunc {
	if d.Pair != nil {
		return d.Pair
	}
	return daemon.Pair
}

func (d Dependencies) runDaemon() RunDaemonFunc {
	if d.RunDaemon != nil {
		return d.RunDaemon
	}
	return daemon.Run
}

func (d Dependencies) inventory() daemon.Inventory {
	if d.Inventory != nil {
		return d.Inventory()
	}
	return daemon.Collect(daemon.HostSystem())
}

// newAuthDaemonCommand is `dym auth daemon`: asks Dymmer for a one-time PIN, shows it, and waits until it is entered
// in Dymmer; the permanent secret that Dymmer then gives is kept for `dym daemon`.
func newAuthDaemonCommand(deps Dependencies) *cobra.Command {
	var flags daemonFlags
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Pair this host with Dymmer with a one-time PIN",
		Long: "Pair this host with Dymmer, so that `dym daemon` can keep it connected without SSH.\n\n" +
			"It shows a PIN that is valid for two minutes. Enter it in Dymmer (Hosts, Add host, via dym) while this\n" +
			"command waits; Dymmer then creates the host and gives this machine a secret that stays in the state file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket, statePath, err := flags.resolve(deps)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			out := cmd.OutOrStdout()
			state, err := deps.pair()(ctx, daemon.PairOptions{
				URL:       socket,
				StatePath: statePath,
				Inventory: deps.inventory(),
				OnPIN: func(pin string, validFor time.Duration) {
					fmt.Fprintf(out, "Pairing code: %s\n", pin)
					fmt.Fprintf(out, "Enter it in Dymmer (Hosts, Add host, via dym) within %s. Waiting...\n", validFor.Round(time.Second))
				},
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "Paired: this machine is now the host %s in Dymmer.\n", state.HostID)
			fmt.Fprintln(out, "Start the daemon with `dym daemon` (the service does it by itself once the state file exists).")
			return nil
		},
	}
	flags.add(cmd)
	return cmd
}

// newDaemonCommand is `dym daemon`: the process that keeps the host connected to Dymmer. It accepts no command from
// Dymmer; it only says that the host is alive and what it is.
func newDaemonCommand(deps Dependencies) *cobra.Command {
	var flags daemonFlags
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Keep this host connected to Dymmer (no SSH needed)",
		Long: "Keep this host connected to Dymmer. The host opens the connection, so it works behind NAT or a firewall.\n\n" +
			"Dymmer shows the host as on while this runs. It does not accept commands: it only tells Dymmer that the host\n" +
			"is alive and what it is. Pair the host first with `dym auth daemon`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket, statePath, err := flags.resolve(deps)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			return deps.runDaemon()(ctx, daemon.RunOptions{URL: socket, StatePath: statePath, Logger: logger})
		},
	}
	flags.add(cmd)
	return cmd
}
