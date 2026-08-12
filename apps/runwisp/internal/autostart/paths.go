// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	// Stat reports the resolved binary's file info (typically os.Stat). When
	// non-nil, ResolveBinary requires the resolved path to be a regular,
	// executable file — the unit bakes this path into a root-run ExecStart, so
	// a dangling symlink or a non-executable target must fail the install rather
	// than be written and boot-looped. Left nil only by pure unit tests that
	// exercise path logic without a real filesystem.
	Stat func(string) (os.FileInfo, error)
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
		// Fail closed: a symlink-resolution error means the executable path does
		// not resolve to a real file. Tolerating it would bake an unresolvable
		// (or dangling) path into a root-run ExecStart.
		r, err := opts.EvalSymlinks(exe)
		if err != nil {
			return "", "", fmt.Errorf("resolve binary %q: %w", exe, err)
		}
		if r != "" {
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

	if opts.Stat != nil {
		info, err := opts.Stat(resolved)
		if err != nil {
			return "", "", fmt.Errorf("resolve binary %q: %w", resolved, err)
		}
		if mode := info.Mode(); !mode.IsRegular() {
			return "", "", fmt.Errorf("resolve binary %q: not a regular file", resolved)
		} else if mode.Perm()&0o111 == 0 {
			return "", "", fmt.Errorf("resolve binary %q: not executable", resolved)
		}
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
	return transientPathReason(p, "/var/tmp is not a durable install location")
}

// transientPathReason returns a short reason string when p lives in a
// location that will not survive a reboot. Empty means "no hard reject".
// varTmpReason lets callers phrase the /var/tmp case for their own risk (a
// binary needs it durable across reboots; a data dir just needs it to survive
// tmpwatch).
func transientPathReason(p, varTmpReason string) string {
	switch {
	case strings.HasPrefix(p, "/tmp/"), p == "/tmp":
		return "/tmp is wiped on reboot"
	case strings.HasPrefix(p, "/var/tmp/"), p == "/var/tmp":
		return varTmpReason
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
	// Explicit is the value the user passed via --data (or "" / ".runwisp"
	// for the default).
	Explicit string
	// ExplicitSet is true when the user actually set --data (vs.
	// taking the cobra default of ".runwisp"). Cobra doesn't expose
	// "was set", so the caller checks Changed() and passes it in.
	ExplicitSet bool

	// HomeDir, XDGDataHome — injected for tests.
	HomeDir     string
	XDGDataHome string

	// Existing tells us whether the default ./.runwisp dir holds a real DB.
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
	// ResolveActionNotice means the caller should print Detail as a neutral,
	// informational line (not a warning) and proceed. Used when a relative
	// --data is silently resolved to an absolute path.
	ResolveActionNotice
)

// defaultDataDirFlag matches cmd_root.go default value for --data.
const defaultDataDirFlag = ".runwisp"

// configFileName is the canonical RunWisp config filename.
const configFileName = "runwisp.toml"

// errResolveConfig is the wrap prefix used by ResolveConfigPath.
const errResolveConfig = "resolve config: %w"

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
		// A relative --data (e.g. ".") is resolved to an absolute path — a
		// boot-launched unit runs with cwd=/, so the absolute form is what we
		// bake in. This mirrors ResolveConfigPath and makes "install into the
		// current directory" (--data .) just work.
		abs := opts.Explicit
		relative := !filepath.IsAbs(abs)
		if relative {
			resolved, err := filepath.Abs(abs)
			if err != nil {
				return ResolveDataDirResult{}, fmt.Errorf("resolve data dir: %w", err)
			}
			abs = resolved
		}
		if reason := transientDataDirReason(abs); reason != "" {
			return ResolveDataDirResult{
				Action: ResolveActionReject,
				Detail: fmt.Sprintf("--data %q is not a durable location: %s.", opts.Explicit, reason),
			}, nil
		}
		res := ResolveDataDirResult{Path: abs, Action: ResolveActionAccept}
		switch {
		case strings.Contains(abs, " "):
			res.Action = ResolveActionWarn
			res.Detail = "data dir path contains spaces; this can confuse systemd Environment= parsing."
		case relative:
			res.Action = ResolveActionNotice
			res.Detail = fmt.Sprintf("Using data dir %s (resolved from %q).", abs, opts.Explicit)
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
	return transientPathReason(p, "/var/tmp may be cleaned by tmpwatch")
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

	if opts.ExplicitSet && opts.Explicit != "" && opts.Explicit != configFileName {
		if filepath.IsAbs(opts.Explicit) {
			return opts.Explicit, nil
		}
		abs, err := filepath.Abs(opts.Explicit)
		if err != nil {
			return "", fmt.Errorf(errResolveConfig, err)
		}
		return abs, nil
	}

	if opts.XDGExists {
		return xdg, nil
	}
	if opts.BareExists {
		abs, err := filepath.Abs(configFileName)
		if err != nil {
			return "", fmt.Errorf(errResolveConfig, err)
		}
		return abs, nil
	}
	// Neither exists — return the XDG path so the error message points
	// the user at a sensible default.
	if xdg != "" {
		return xdg, nil
	}
	abs, err := filepath.Abs(configFileName)
	if err != nil {
		return "", fmt.Errorf(errResolveConfig, err)
	}
	return abs, nil
}

// XDGConfigPath returns the XDG-conformant runwisp.toml path for the
// given home / XDG_CONFIG_HOME. Empty when both are empty.
func XDGConfigPath(home, xdg string) string {
	if xdg != "" {
		return filepath.Join(xdg, "runwisp", configFileName)
	}
	if home != "" {
		return filepath.Join(home, ".config", "runwisp", configFileName)
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

// envPathOrDefault returns $PATH, or fallback when it's unset — a
// non-interactive service manager may not propagate a login PATH to the unit
// it launches.
func envPathOrDefault(fallback string) string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return fallback
}
