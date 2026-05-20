// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveBinaryOptions configures ResolveBinary.
type ResolveBinaryOptions struct {
	// ExecutablePath is os.Executable() — injected for testability.
	ExecutablePath string
	// EvalSymlinks resolves symlinks (typically filepath.EvalSymlinks).
	EvalSymlinks func(string) (string, error)
	// HomeDir is the user's home directory (injected for tests).
	HomeDir string
}

// ResolveBinary picks the binary path baked into the unit. It rejects
// transient locations outright (no --force escape) and warns on
// awkward-but-acceptable ones. The returned warning is non-empty when
// the caller should surface a "consider copying to ~/.local/bin" hint.
func ResolveBinary(opts ResolveBinaryOptions) (path, warning string, err error) {
	exe := opts.ExecutablePath
	if exe == "" {
		return "", "", errors.New("resolve binary: empty executable path")
	}
	resolved := exe
	if opts.EvalSymlinks != nil {
		r, err := opts.EvalSymlinks(exe)
		if err == nil && r != "" {
			resolved = r
		}
	}
	if !filepath.IsAbs(resolved) {
		abs, err := filepath.Abs(resolved)
		if err != nil {
			return "", "", fmt.Errorf("resolve binary: %w", err)
		}
		resolved = abs
	}

	if reason := transientBinaryReason(resolved); reason != "" {
		return "", "", fmt.Errorf(
			"refusing to install service pointing at %s — %s; copy the binary to a stable path (e.g. ~/.local/bin/runwisp) and retry",
			resolved, reason,
		)
	}

	warning = awkwardBinaryWarning(resolved, opts.HomeDir)
	return resolved, warning, nil
}

// transientBinaryReason returns a short reason string when the path
// lives in a location that will not survive a reboot (or the next
// `go run`). Empty means "no hard reject".
func transientBinaryReason(p string) string {
	// `go run` first — a typical path lives under /tmp/, so the
	// /tmp branch would otherwise swallow the more specific reason.
	if strings.Contains(p, "/go-build") && strings.Contains(p, "/exe/") {
		return "this looks like a `go run` cache path"
	}
	switch {
	case strings.HasPrefix(p, "/tmp/"), p == "/tmp":
		return "/tmp is wiped on reboot"
	case strings.HasPrefix(p, "/var/tmp/"), p == "/var/tmp":
		return "/var/tmp is not a durable install location"
	case strings.HasPrefix(p, "/dev/shm/"), p == "/dev/shm":
		return "/dev/shm is a tmpfs that is wiped on reboot"
	}
	return ""
}

// awkwardBinaryWarning returns a non-empty hint when the path will
// work but is unfortunate (under ~/go/bin, ~/.cache, contains spaces).
func awkwardBinaryWarning(p, home string) string {
	if strings.Contains(p, " ") {
		return "binary path contains spaces; consider copying to ~/.local/bin/runwisp"
	}
	if home != "" {
		if strings.HasPrefix(p, filepath.Join(home, "go", "bin")+string(filepath.Separator)) {
			return "binary is under ~/go/bin; rebuilds will replace it. Consider copying to ~/.local/bin/runwisp."
		}
		if strings.HasPrefix(p, filepath.Join(home, ".cache")+string(filepath.Separator)) {
			return "binary is under ~/.cache; cache cleanup may delete it. Consider copying to ~/.local/bin/runwisp."
		}
	}
	return ""
}

// ResolveDataDirOptions configures ResolveDataDir.
type ResolveDataDirOptions struct {
	// Explicit is the value the user passed via --data (or "" / "data"
	// for the default).
	Explicit string
	// ExplicitSet is true when the user actually set --data (vs.
	// taking the cobra default of "data"). Cobra doesn't expose
	// "was set", so the caller checks Changed() and passes it in.
	ExplicitSet bool

	// HomeDir, XDGDataHome — injected for tests.
	HomeDir     string
	XDGDataHome string

	// Existing tells us whether the default ./data dir holds a real DB.
	// Injected by the caller (which has the FileSystem) so this function
	// stays free of I/O dependencies.
	BareDefaultHasDB bool
}

// ResolveDataDirResult breaks the resolution into "what to use" plus
// any guidance the caller should surface (prompt / warn / error).
type ResolveDataDirResult struct {
	// Path is the resolved absolute data dir.
	Path string
	// Action describes what the caller should do before accepting Path.
	Action ResolveAction
	// Detail is a human-readable explanation tied to Action. For
	// ResolveActionPrompt it is the full Y/n question to ask.
	Detail string
}

// ResolveAction tells the caller what to do with the resolved path.
type ResolveAction int

const (
	// ResolveActionAccept means the path can be used silently.
	ResolveActionAccept ResolveAction = iota
	// ResolveActionPrompt means the caller should ask the Y/n question
	// in Detail (defaults to yes). Used both for "keep existing data
	// at <abs>?" and "use default location <xdg>?".
	ResolveActionPrompt
	// ResolveActionReject means the caller should error out.
	ResolveActionReject
	// ResolveActionWarn means the caller should print Detail and proceed.
	ResolveActionWarn
)

// defaultDataDirFlag matches cmd_root.go default value for --data.
const defaultDataDirFlag = "data"

// xdgDataDir returns the XDG-conformant data dir for RunWisp.
func xdgDataDir(home, xdg string) string {
	if xdg != "" {
		return filepath.Join(xdg, "runwisp")
	}
	if home != "" {
		return filepath.Join(home, ".local", "share", "runwisp")
	}
	return ""
}

// ResolveDataDir decides where the unit should point. It does not
// touch the filesystem — callers feed it the "DB exists at default
// path" signal so this function stays pure.
func ResolveDataDir(opts ResolveDataDirOptions) (ResolveDataDirResult, error) {
	xdg := xdgDataDir(opts.HomeDir, opts.XDGDataHome)

	if opts.ExplicitSet && opts.Explicit != "" && opts.Explicit != defaultDataDirFlag {
		if !filepath.IsAbs(opts.Explicit) {
			abs, err := filepath.Abs(opts.Explicit)
			if err != nil {
				return ResolveDataDirResult{}, fmt.Errorf("resolve data dir: %w", err)
			}
			return ResolveDataDirResult{
				Action: ResolveActionReject,
				Detail: fmt.Sprintf("--data %q is relative; a boot-launched unit has cwd=/ and would not find it. Re-run with --data %s.", opts.Explicit, abs),
			}, nil
		}
		if reason := transientDataDirReason(opts.Explicit); reason != "" {
			return ResolveDataDirResult{
				Action: ResolveActionReject,
				Detail: fmt.Sprintf("--data %q is not a durable location: %s.", opts.Explicit, reason),
			}, nil
		}
		res := ResolveDataDirResult{Path: opts.Explicit, Action: ResolveActionAccept}
		if strings.Contains(opts.Explicit, " ") {
			res.Action = ResolveActionWarn
			res.Detail = "data dir path contains spaces; this can confuse systemd Environment= parsing."
		}
		return res, nil
	}

	// Default (or empty) — the user did not pin a data dir.
	if opts.BareDefaultHasDB {
		abs, err := filepath.Abs(defaultDataDirFlag)
		if err != nil {
			return ResolveDataDirResult{}, fmt.Errorf("resolve data dir: %w", err)
		}
		return ResolveDataDirResult{
			Path:   abs,
			Action: ResolveActionPrompt,
			Detail: fmt.Sprintf("Use existing data at %s?", abs),
		}, nil
	}

	if xdg == "" {
		return ResolveDataDirResult{}, errors.New("resolve data dir: HOME is unset and XDG_DATA_HOME is unset")
	}
	return ResolveDataDirResult{
		Path:   xdg,
		Action: ResolveActionPrompt,
		Detail: fmt.Sprintf("Use default data location %s?", xdg),
	}, nil
}

// transientDataDirReason mirrors transientBinaryReason for data dirs.
func transientDataDirReason(p string) string {
	switch {
	case strings.HasPrefix(p, "/tmp/"), p == "/tmp":
		return "/tmp is wiped on reboot"
	case strings.HasPrefix(p, "/var/tmp/"), p == "/var/tmp":
		return "/var/tmp may be cleaned by tmpwatch"
	case strings.HasPrefix(p, "/dev/shm/"), p == "/dev/shm":
		return "/dev/shm is a tmpfs that is wiped on reboot"
	}
	return ""
}

// ResolveConfigOptions configures ResolveConfigPath.
type ResolveConfigOptions struct {
	Explicit    string
	ExplicitSet bool
	HomeDir     string
	XDGConfHome string
	// XDGExists / BareExists are injected by the caller.
	XDGExists  bool
	BareExists bool
}

// ResolveConfigPath returns the runwisp.toml path baked into the unit.
// The caller is responsible for verifying the file actually exists
// (autostart never creates config files).
func ResolveConfigPath(opts ResolveConfigOptions) (string, error) {
	xdg := XDGConfigPath(opts.HomeDir, opts.XDGConfHome)

	if opts.ExplicitSet && opts.Explicit != "" && opts.Explicit != "runwisp.toml" {
		if filepath.IsAbs(opts.Explicit) {
			return opts.Explicit, nil
		}
		abs, err := filepath.Abs(opts.Explicit)
		if err != nil {
			return "", fmt.Errorf("resolve config: %w", err)
		}
		return abs, nil
	}

	if opts.XDGExists {
		return xdg, nil
	}
	if opts.BareExists {
		abs, err := filepath.Abs("runwisp.toml")
		if err != nil {
			return "", fmt.Errorf("resolve config: %w", err)
		}
		return abs, nil
	}
	// Neither exists — return the XDG path so the error message points
	// the user at a sensible default.
	if xdg != "" {
		return xdg, nil
	}
	abs, err := filepath.Abs("runwisp.toml")
	if err != nil {
		return "", fmt.Errorf("resolve config: %w", err)
	}
	return abs, nil
}

// XDGConfigPath returns the XDG-conformant runwisp.toml path for the
// given home / XDG_CONFIG_HOME. Empty when both are empty.
func XDGConfigPath(home, xdg string) string {
	if xdg != "" {
		return filepath.Join(xdg, "runwisp", "runwisp.toml")
	}
	if home != "" {
		return filepath.Join(home, ".config", "runwisp", "runwisp.toml")
	}
	return ""
}

// WSLDetectInput is the input to DetectWSL — split out so tests can
// supply synthetic values instead of touching real env / /proc.
type WSLDetectInput struct {
	WSLDistroName string
	OSRelease     string
}

// DetectWSL reports whether the current environment is WSL.
func DetectWSL(in WSLDetectInput) bool {
	if in.WSLDistroName != "" {
		return true
	}
	lower := strings.ToLower(in.OSRelease)
	if strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl") {
		return true
	}
	return false
}

// DetectWSLFromEnv reads the conventional signals from the host.
func DetectWSLFromEnv() bool {
	data, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return DetectWSL(WSLDetectInput{
		WSLDistroName: os.Getenv("WSL_DISTRO_NAME"),
		OSRelease:     string(data),
	})
}

// UserHomeDir returns the home directory, preferring $HOME for
// determinism in unit/service contexts.
func UserHomeDir() (string, error) {
	if h := os.Getenv("HOME"); h != "" {
		return h, nil
	}
	return os.UserHomeDir()
}
