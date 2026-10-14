// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// composeProjectLabel is the label Docker Compose stamps on every container it
// creates, holding the project the container belongs to.
const composeProjectLabel = "com.docker.compose.project"

// containerIDInMountinfo finds the container's own ID in /proc/self/mountinfo.
// The runtime bind-mounts /etc/hostname, /etc/hosts and /etc/resolv.conf from a
// directory named after the container (`.../containers/<id>/hostname` on Docker,
// `.../overlay-containers/<id>/userdata/hostname` on Podman). Unlike the
// hostname it survives a `hostname:` override, and unlike /proc/self/cgroup it
// works under a private cgroup namespace.
var containerIDInMountinfo = regexp.MustCompile(`containers/([0-9a-f]{64})/`)

// selfContainerRefs lists what may name RunWisp's own container to
// `docker inspect`: the ID from mountinfo, then the hostname (Docker's default
// hostname is the short ID).
func selfContainerRefs() []string {
	var refs []string
	if info, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if m := containerIDInMountinfo.FindSubmatch(info); m != nil {
			refs = append(refs, string(m[1]))
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		refs = append(refs, host)
	}
	return refs
}

// ownProject returns the compose project RunWisp's own container belongs to,
// read from that container's compose label. It lets a task say
// `compose_service = "app"` with no compose_file when RunWisp runs as a service
// of the stack it schedules jobs for: compose exec needs only the project name,
// so the project directory doesn't have to be mounted into RunWisp.
//
// Only a success is cached. A failed lookup (the docker socket isn't mounted
// yet, say) is retried by the next run.
func (b *ComposeBackend) ownProject(ctx context.Context) (string, error) {
	b.selfMu.Lock()
	defer b.selfMu.Unlock()
	if b.selfProject != "" {
		return b.selfProject, nil
	}
	refs := b.selfRefs
	if refs == nil {
		refs = selfContainerRefs
	}
	for _, ref := range refs() {
		lookupCtx, cancel := context.WithTimeout(ctx, composeHousekeepingTimeout)
		out, err := exec.CommandContext(lookupCtx, b.dockerCmd, "inspect", "--type", "container",
			"--format", `{{index .Config.Labels "`+composeProjectLabel+`"}}`, ref).Output()
		cancel()
		if project := strings.TrimSpace(string(out)); err == nil && project != "" {
			b.selfProject = project
			return project, nil
		}
	}
	return "", errors.New("compose_service without compose_file needs RunWisp to run as a service of the compose project it targets, " +
		"and no " + composeProjectLabel + " label was found on RunWisp's own container " +
		"(is /var/run/docker.sock mounted? did you start it with `docker run` rather than compose?); set compose_file to target a stack by its file")
}
