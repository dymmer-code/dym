package daemon

import (
	"context"
	"strings"
	"time"
)

// A service manager: how to ask the status of one, and how to change one. The scripts of Dymmer did these things with
// a shell; here each is a list of arguments, and the name that Dymmer gives is checked before anything else.
type manager struct {
	key     string                              // the key in `services:` of the configuration
	actions []string                            // what can be asked besides the status
	status  func(env OpEnv, name string) Result // never changes anything
	command func(action, name string) []string  // how to change it
}

const statusTimeout = 15 * time.Second

var (
	systemctlManager = manager{
		key:     "systemctl",
		actions: []string{"start", "stop", "restart", "reload"},
		command: func(action, name string) []string { return []string{"systemctl", action, "--", name} },
		status: func(env OpEnv, name string) Result {
			load := env.run(ExecSpec{Argv: []string{"systemctl", "show", "-p", "LoadState", "--value", "--", name}, Timeout: statusTimeout})
			if load.Err != nil || load.ExitCode != 0 || load.Output == "not-found" || load.Output == "" {
				return serviceResult("FAIL", "UNKNOWN", name, "The service doesn't exist or it cannot be queried.")
			}
			if env.run(ExecSpec{Argv: []string{"systemctl", "is-active", "--quiet", "--", name}, Timeout: statusTimeout}).ExitCode == 0 {
				return serviceResult("OK", "OK", name, "The service is running.")
			}
			return serviceResult("OK", "NO", name, "The service is stopped.")
		},
	}

	serviceManager = manager{
		key:     "service",
		actions: []string{"start", "stop", "restart", "reload"},
		command: func(action, name string) []string { return []string{"service", name, action} },
		status:  statusByExit([]string{"service"}, "status", "The service is running.", "The service is stopped."),
	}

	initDManager = manager{
		key:     "init_d",
		actions: []string{"start", "stop", "restart"},
		command: func(action, name string) []string { return []string{"/etc/init.d/" + name, action} },
		status: func(env OpEnv, name string) Result {
			if env.run(ExecSpec{Argv: []string{"/etc/init.d/" + name, "status"}, Timeout: statusTimeout}).ExitCode == 0 {
				return serviceResult("OK", "OK", name, "Service is running.")
			}
			return serviceResult("OK", "NO", name, "Service is stopped.")
		},
	}

	dockerManager = manager{
		key:     "docker",
		actions: []string{"start", "stop", "restart"},
		command: func(action, name string) []string { return []string{"docker", action, name} },
		status: func(env OpEnv, name string) Result {
			r := env.run(ExecSpec{Argv: []string{"docker", "inspect", "--format={{.State.Status}}", name}, Timeout: statusTimeout})
			switch {
			case r.Err != nil || r.ExitCode != 0:
				return serviceResult("FAIL", "UNKNOWN", name, "Container "+name+" not found.")
			case r.Output == "running":
				return serviceResult("OK", "OK", name, "Container is running.")
			}
			return serviceResult("OK", "NO", name, "Container status: "+r.Output+".")
		},
	}

	pm2Manager = manager{
		key:     "pm2",
		actions: []string{"start", "stop", "restart", "reload"},
		command: func(action, name string) []string { return []string{"pm2", action, name} },
		status: func(env OpEnv, name string) Result {
			out := env.run(ExecSpec{Argv: []string{"pm2", "describe", name}, Timeout: statusTimeout}).Output
			switch {
			case strings.Contains(out, "online"):
				return serviceResult("OK", "OK", name, "Process is online.")
			case strings.Contains(out, "stopped"), strings.Contains(out, "errored"):
				return serviceResult("OK", "NO", name, "Process is stopped.")
			}
			return serviceResult("FAIL", "UNKNOWN", name, "Process "+name+" not found.")
		},
	}

	supervisordManager = manager{
		key:     "supervisord",
		actions: []string{"start", "stop", "restart"},
		command: func(action, name string) []string { return []string{"supervisorctl", action, name} },
		status: func(env OpEnv, name string) Result {
			out := env.run(ExecSpec{Argv: []string{"supervisorctl", "status", name}, Timeout: statusTimeout}).Output
			switch {
			case strings.Contains(out, "RUNNING"):
				return serviceResult("OK", "OK", name, "Process is running.")
			case strings.Contains(out, "STOPPED"), strings.Contains(out, "EXITED"), strings.Contains(out, "FATAL"):
				return serviceResult("OK", "NO", name, "Process is stopped.")
			}
			return serviceResult("FAIL", "UNKNOWN", name, "Cannot get status for "+name+".")
		},
	}
)

// statusByExit is the status of a manager that answers with the exit code of `<command> <name> status`... or, for
// `service`, `service <name> status`.
func statusByExit(base []string, verb, running, stopped string) func(OpEnv, string) Result {
	return func(env OpEnv, name string) Result {
		argv := append(append([]string{}, base...), name, verb)
		if env.run(ExecSpec{Argv: argv, Timeout: statusTimeout}).ExitCode == 0 {
			return serviceResult("OK", "OK", name, running)
		}
		return serviceResult("OK", "NO", name, stopped)
	}
}

func serviceResult(status, output, name, message string) Result {
	return Result{Status: status, Output: output, Service: name, Message: message}
}

// run runs a command with the executor of the environment.
func (e OpEnv) run(spec ExecSpec) ExecResult {
	if e.Exec == nil {
		return ExecResult{ExitCode: -1}
	}
	return e.Exec(context.Background(), spec)
}

// serviceOperation makes the operation `<script>.sh <name> <action>` of a manager. The name is checked before
// anything else; the status can be asked for any valid name; changing needs the name in the configuration.
func serviceOperation(m manager) Operation {
	return func(env OpEnv, args []string) Result {
		if len(args) != 2 {
			return fail("Missing parameters.")
		}
		name, action := args[0], args[1]
		if !ValidName(name) {
			return serviceResult("FAIL", "UNKNOWN", name, "That is not a valid name.")
		}
		if action == "status" {
			return m.status(env, name)
		}
		if !includes(m.actions, action) {
			return serviceResult("FAIL", "UNKNOWN", name, "Disallowed action.")
		}

		config, err := env.config()
		if err != nil {
			return serviceResult("FAIL", "ERROR", name, "The configuration of this host cannot be read: "+err.Error())
		}
		if !config.AllowsService(m.key, name) {
			return serviceResult("FAIL", "ERROR", name, "The configuration of this host does not allow "+action+" on "+name+".")
		}

		return changeService(env, config, m.command(action, name), "", action, name)
	}
}

// changeService runs the command that changes a service, with the privilege the owner wrote, and says how it went.
func changeService(env OpEnv, config Config, argv []string, dir, action, name string) Result {
	r := env.run(ExecSpec{Argv: append(append([]string{}, config.Privilege...), argv...), Dir: dir, Timeout: time.Minute})
	if r.Err == nil && r.ExitCode == 0 {
		return serviceResult("OK", "OK", name, "Operation "+action+" completed successfully.")
	}
	return serviceResult("FAIL", "ERROR", name, "Error trying to run "+action+" on "+name+".")
}

// composeOperation is `docker_compose.sh <path> <service> <action>`: every operation needs the project, and the service
// in it, in the configuration.
func composeOperation(env OpEnv, args []string) Result {
	if len(args) != 3 {
		return fail("Missing parameters.")
	}
	path, name, action := args[0], args[1], args[2]
	if !ValidName(name) {
		return serviceResult("FAIL", "UNKNOWN", name, "That is not a valid name.")
	}

	config, err := env.config()
	if err != nil {
		return serviceResult("FAIL", "ERROR", name, "The configuration of this host cannot be read: "+err.Error())
	}
	project, declared := config.ComposeProject(path)
	if !declared || !includes(project.Services, name) {
		return serviceResult("FAIL", "ERROR", name, "The configuration of this host does not declare "+name+" in "+path+".")
	}

	switch action {
	case "status":
		out := env.run(ExecSpec{Argv: []string{"docker", "compose", "ps", name}, Dir: path, Timeout: statusTimeout}).Output
		if strings.Contains(out, "running") || strings.Contains(out, "Up") {
			return serviceResult("OK", "OK", name, "Service is running.")
		}
		return serviceResult("OK", "NO", name, "Service is stopped.")
	case "start":
		return changeService(env, config, []string{"docker", "compose", "up", "-d", name}, path, action, name)
	case "stop", "restart":
		return changeService(env, config, []string{"docker", "compose", action, name}, path, action, name)
	}
	return serviceResult("FAIL", "UNKNOWN", name, "Disallowed action.")
}
