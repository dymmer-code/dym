package daemon

import (
	"strconv"
	"strings"
	"time"
)

// actionOperation is `action.sh <name>`: it runs what the owner wrote in the configuration under that name. Dymmer
// gives the name and nothing else; the command, the directory, the environment and the time are the owner's.
func actionOperation(env OpEnv, args []string) Result {
	if len(args) != 1 {
		return fail("An action needs its name, and only it.")
	}
	name := args[0]
	if !ValidName(name) {
		return fail("That is not a valid name for an action.")
	}

	config, err := env.config()
	if err != nil {
		return fail("The configuration of this host cannot be read: " + err.Error())
	}
	action, declared := config.Actions[name]
	if !declared {
		return fail("The configuration of this host has no action called " + name + ".")
	}
	timeout, err := action.Duration()
	if err != nil {
		return fail("The time of the action is not valid: " + err.Error())
	}

	argv := append([]string{}, action.Command...)
	if action.Privileged {
		argv = append(append([]string{}, config.Privilege...), argv...)
	}
	r := env.run(ExecSpec{Argv: argv, Dir: action.Dir, Env: action.Env, Timeout: timeout})

	message := lastChars(r.Output, 2000)
	switch {
	case r.Err != nil && r.ExitCode == -1 && strings.Contains(r.Err.Error(), "deadline"):
		return fail("The action took longer than " + timeout.Round(time.Second).String() + ".")
	case r.Err != nil:
		return fail("The action could not be run: " + r.Err.Error())
	case r.ExitCode != 0:
		return fail("The action ended with code " + itoa(r.ExitCode) + ": " + message)
	}
	return ok(message)
}

func lastChars(text string, max int) string {
	if runes := []rune(text); len(runes) > max {
		return "…" + string(runes[len(runes)-max:])
	}
	return text
}

func itoa(n int) string { return strconv.Itoa(n) }
