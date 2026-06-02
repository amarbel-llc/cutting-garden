package capture_plugin

import (
	"os"
	"runtime"
	"strings"
)

// HostInfo describes the machine a capture ran on. It feeds the
// `jcs-cutting_garden-capture-environment-host-v1` node body and is part
// of capture identity: two captures on the same machine share a host
// markl-id.
type HostInfo struct {
	OS     string
	Kernel string
	Arch   string
	Libc   string
}

// GatherHost collects the current host's identity fields. os and arch
// come from the Go runtime; kernel is read from the OS where cheaply
// available (Linux `/proc/sys/kernel/osrelease`); libc detection is not
// attempted and reports "unknown" — RFC 0002 requires the field, not a
// specific provenance, and a wrong value would forge identity. Refining
// kernel/libc detection is a documented follow-up.
func GatherHost() HostInfo {
	return HostInfo{
		OS:     runtime.GOOS,
		Kernel: gatherKernel(),
		Arch:   runtime.GOARCH,
		Libc:   "unknown",
	}
}

func gatherKernel() string {
	if runtime.GOOS == "linux" {
		if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	return "unknown"
}

func (h HostInfo) body() map[string]any {
	return map[string]any{
		"os":     h.OS,
		"kernel": h.Kernel,
		"arch":   h.Arch,
		"libc":   h.Libc,
	}
}

// BinaryInfo identifies the plugin binary that produced a capture. Name
// and Version are required; Digest (markl id of the binary itself) is
// optional and, when present, makes the binary's bytes part of identity
// — RFC 0002 RECOMMENDS it only under deterministic builds, so callers
// on non-deterministic builds leave it empty to avoid identity churn.
type BinaryInfo struct {
	Name           string
	Version        string
	Digest         string
	CapabilitiesId string
}

func (b BinaryInfo) bodyMap() map[string]any {
	m := map[string]any{
		"name":    b.Name,
		"version": b.Version,
	}
	if b.Digest != "" {
		m["digest"] = b.Digest
	}
	if b.CapabilitiesId != "" {
		m["capabilities_id"] = b.CapabilitiesId
	}
	return m
}
