package main

// The store-path flags default to fixed locations under $HOME/.timesheet so
// the daily commands never have to repeat them: --config defaults to
// $HOME/.timesheet/config.json and --env-file to $HOME/.timesheet/.env.local.
// An explicit flag always wins verbatim, so tests, the Pi launcher and
// alternate setups keep working unchanged; serve still requires both flags
// because the launcher supplies them.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultConfigName = "config.json"
	defaultEnvName    = ".env.local"
)

// homeTimesheetPath returns <home>/.timesheet/<name>. When the home directory
// cannot be resolved the error names the fix, so the message is actionable
// wherever the caller surfaces it.
func homeTimesheetPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve the default $HOME/.timesheet/%s ($HOME is not set); pass the flag explicitly", name)
	}
	return filepath.Join(home, ".timesheet", name), nil
}

// resolveConfigPath returns the configuration path a command loads: the
// explicit --config verbatim, or the default $HOME/.timesheet/config.json. A
// missing default is an error that names the file and the fix — LoadConfig's
// own read failure carries no path, so an unconfigured machine would otherwise
// fail with a message that does not say what to create or pass.
func resolveConfigPath(name, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	def, err := homeTimesheetPath(defaultConfigName)
	if err != nil {
		return "", fmt.Errorf("%s: %v", name, err)
	}
	if _, err := os.Stat(def); err != nil {
		return "", fmt.Errorf("%s: no configuration at %s; pass --config <path>", name, def)
	}
	return def, nil
}

// resolveEnvFile returns the environment-file path: the explicit --env-file
// verbatim (an unreadable explicit file still fails in LoadEnvFile), or the
// default $HOME/.timesheet/.env.local when it exists. A missing or
// unresolvable default is NOT an error: the command runs with no file values,
// exactly as an omitted --env-file always has, and ops needing credentials
// fail closed with their own errors naming the fix.
func resolveEnvFile(name, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	def, err := homeTimesheetPath(defaultEnvName)
	if err != nil {
		return "", nil
	}
	if _, err := os.Stat(def); err != nil {
		return "", nil
	}
	return def, nil
}

// resolveStoreFlags resolves both store-path flags together. Every parser
// calls it right after flag parsing, so the rest of the command sees resolved
// paths only and no command ever re-implements the defaulting rules.
func resolveStoreFlags(name, configPath, envFile string) (string, string, error) {
	config, err := resolveConfigPath(name, configPath)
	if err != nil {
		return "", "", err
	}
	env, err := resolveEnvFile(name, envFile)
	if err != nil {
		return "", "", err
	}
	return config, env, nil
}
