package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is what the owner of this host allows Dymmer to do beyond reading. It is a file that only the owner writes
// (root, readable by the daemon), and it is read again for every order, so that a change needs no restart. Without the
// file nothing that changes the host can be asked: the daemon only answers readings.
//
//	privilege: [sudo, -n]          # put before the commands that change something (optional)
//	services:                      # what Dymmer may start, stop, restart or reload; reading the status needs no entry
//	  systemctl: [nginx, myapp]
//	  docker: [web]
//	compose:                       # docker compose projects; every operation on one needs its entry
//	  - path: /srv/app
//	    services: [web, worker]
//	actions:                       # what an action of a project or a command of a deployment may run, by name
//	  deploy-site:
//	    command: [/usr/local/bin/deploy-site, --prod]
//	    dir: /srv/site
//	    env: {MODE: prod}
//	    timeout: 5m
//	    privileged: true
//	logs:                          # the only files and units whose lines Dymmer may read
//	  files: [/var/log/nginx/access.log]
//	  units: [nginx]
type Config struct {
	Privilege []string                `yaml:"privilege"`
	Services  map[string][]string     `yaml:"services"`
	Compose   []ComposeProject        `yaml:"compose"`
	Actions   map[string]ActionConfig `yaml:"actions"`
	Logs      LogsConfig              `yaml:"logs"`
}

// ComposeProject is a docker compose project, and the services of it that Dymmer may handle.
type ComposeProject struct {
	Path     string   `yaml:"path"`
	Services []string `yaml:"services"`
}

// ActionConfig is a command that has a name. Dymmer asks for the name; what runs is written here.
type ActionConfig struct {
	Command    []string          `yaml:"command"`
	Dir        string            `yaml:"dir"`
	Env        map[string]string `yaml:"env"`
	Timeout    string            `yaml:"timeout"`
	Privileged bool              `yaml:"privileged"`
}

// LogsConfig lists what may be read.
type LogsConfig struct {
	Files []string `yaml:"files"`
	Units []string `yaml:"units"`
}

// DefaultConfigPath is where the owner writes it: /etc/dym/daemon.yaml for root (the service), the user's config otherwise.
func DefaultConfigPath() (string, error) {
	if os.Geteuid() == 0 {
		return "/etc/dym/daemon.yaml", nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot find a place for the configuration: %w", err)
	}
	return filepath.Join(dir, "dym", "daemon.yaml"), nil
}

// A name Dymmer may give: letters, digits and . _ @ : -, never starting with a dash, so that it can never be read as an
// option by the program it is given to.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._:-]{0,127}$`)

// ValidName says whether a text can be the name of a service, a container or an action.
func ValidName(name string) bool { return namePattern.MatchString(name) }

var serviceManagers = []string{"systemctl", "service", "init_d", "docker", "pm2", "supervisord"}

// LoadConfig reads and checks the file. A file that is not there is an empty configuration, not an error; a file that
// is wrong is an error and none of it is used, so a typo can never allow more than was written.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}

	var c Config
	decoder := yaml.NewDecoder(bytesReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil && !errors.Is(err, errEmptyDocument) {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Validate checks every entry. The first thing wrong is reported.
func (c Config) Validate() error {
	for manager, names := range c.Services {
		if !includes(serviceManagers, manager) {
			return fmt.Errorf("services: %q is not a manager (%v)", manager, serviceManagers)
		}
		for _, name := range names {
			if !ValidName(name) {
				return fmt.Errorf("services.%s: %q is not a valid name", manager, name)
			}
		}
	}
	for _, p := range c.Compose {
		if !filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path {
			return fmt.Errorf("compose: %q must be a clean absolute path", p.Path)
		}
		for _, name := range p.Services {
			if !ValidName(name) {
				return fmt.Errorf("compose %s: %q is not a valid name", p.Path, name)
			}
		}
	}
	for name, a := range c.Actions {
		if !ValidName(name) {
			return fmt.Errorf("actions: %q is not a valid name", name)
		}
		if len(a.Command) == 0 || a.Command[0] == "" {
			return fmt.Errorf("actions.%s: it needs a command", name)
		}
		if a.Dir != "" && (!filepath.IsAbs(a.Dir) || filepath.Clean(a.Dir) != a.Dir) {
			return fmt.Errorf("actions.%s: dir must be a clean absolute path", name)
		}
		if _, err := a.Duration(); err != nil {
			return fmt.Errorf("actions.%s: timeout: %w", name, err)
		}
		if a.Privileged && len(c.Privilege) == 0 {
			return fmt.Errorf("actions.%s: it is privileged, and the file has no privilege", name)
		}
	}
	for _, path := range c.Logs.Files {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("logs.files: %q must be a clean absolute path", path)
		}
	}
	for _, unit := range c.Logs.Units {
		if !ValidName(unit) {
			return fmt.Errorf("logs.units: %q is not a valid name", unit)
		}
	}
	return nil
}

const (
	defaultActionTimeout = time.Minute
	maxActionTimeout     = 30 * time.Minute
)

// Duration is how long the action may run: what it says, one minute without it, never more than half an hour.
func (a ActionConfig) Duration() (time.Duration, error) {
	if a.Timeout == "" {
		return defaultActionTimeout, nil
	}
	d, err := time.ParseDuration(a.Timeout)
	if err != nil {
		return 0, err
	}
	if d <= 0 || d > maxActionTimeout {
		return 0, fmt.Errorf("%s is not between 1ns and %s", a.Timeout, maxActionTimeout)
	}
	return d, nil
}

// AllowsService says whether Dymmer may change that service, container or process.
func (c Config) AllowsService(manager, name string) bool {
	return includes(c.Services[manager], name)
}

// ComposeProject finds a project by its path.
func (c Config) ComposeProject(path string) (ComposeProject, bool) {
	for _, p := range c.Compose {
		if p.Path == path {
			return p, true
		}
	}
	return ComposeProject{}, false
}

// ActionNames lists the actions, sorted.
func (c Config) ActionNames() []string {
	names := make([]string, 0, len(c.Actions))
	for name := range c.Actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func includes(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}
