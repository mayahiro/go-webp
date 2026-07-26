package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type repositoryMetadata struct {
	commit string
	dirty  bool
}

type hostMetadata struct {
	cpuModel  string
	osVersion string
}

func currentRepositoryMetadata() (repositoryMetadata, error) {
	commit, err := metadataCommand("git", "rev-parse", "HEAD")
	if err != nil {
		return repositoryMetadata{}, fmt.Errorf("resolve repository commit: %w", err)
	}
	status, err := metadataCommand("git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return repositoryMetadata{}, fmt.Errorf("resolve repository dirty state: %w", err)
	}
	return repositoryMetadata{commit: commit, dirty: status != ""}, nil
}

func currentHostMetadata() hostMetadata {
	metadata := hostMetadata{cpuModel: "unknown", osVersion: runtime.GOOS}
	switch runtime.GOOS {
	case "darwin":
		if value, err := metadataCommand("sysctl", "-n", "machdep.cpu.brand_string"); err == nil && value != "" {
			metadata.cpuModel = value
		} else if value, err := metadataCommand("sysctl", "-n", "hw.model"); err == nil && value != "" {
			metadata.cpuModel = value
		} else if value, err := metadataCommand("system_profiler", "SPHardwareDataType", "-detailLevel", "mini"); err == nil {
			if value := cpuModelFromSystemProfiler(value); value != "" {
				metadata.cpuModel = value
			}
		}
		if value, err := metadataCommand("sw_vers", "-productVersion"); err == nil && value != "" {
			metadata.osVersion = "macOS " + value
		}
	case "linux":
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			if value := cpuModelFromProcCPUInfo(string(data)); value != "" {
				metadata.cpuModel = value
			}
		}
		if value, err := metadataCommand("uname", "-sr"); err == nil && value != "" {
			metadata.osVersion = value
		}
	case "windows":
		if value := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER")); value != "" {
			metadata.cpuModel = value
		}
		if value, err := metadataCommand("cmd", "/c", "ver"); err == nil && value != "" {
			metadata.osVersion = value
		}
	default:
		if value, err := metadataCommand("uname", "-sr"); err == nil && value != "" {
			metadata.osVersion = value
		}
	}
	return metadata
}

func cpuModelFromSystemProfiler(data string) string {
	for _, key := range []string{"chip", "processor name"} {
		for _, line := range strings.Split(data, "\n") {
			name, value, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(strings.TrimSpace(name), key) {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func cpuModelFromProcCPUInfo(data string) string {
	for _, key := range []string{"model name", "hardware", "processor"} {
		for _, line := range strings.Split(data, "\n") {
			name, value, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(strings.TrimSpace(name), key) {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func metadataCommand(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %w: %s", append([]string{name}, args...), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
