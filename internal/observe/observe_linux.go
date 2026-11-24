//go:build linux

package observe

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("observe read counter: %w", err)
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("observe decode counter: %w", err)
	}
	return value, nil
}
func memoryUsage() (Memory, error) {
	limit, err := readUint("/sys/fs/cgroup/memory.max")
	if err == nil && limit > 0 && limit < 1<<60 {
		if used, err := readUint("/sys/fs/cgroup/memory.current"); err == nil {
			return Memory{Used: used, Limit: &limit, Source: "container"}, nil
		}
	}
	limit, err = readUint("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if err == nil && limit > 0 && limit < 1<<60 {
		if used, err := readUint("/sys/fs/cgroup/memory/memory.usage_in_bytes"); err == nil {
			return Memory{Used: used, Limit: &limit, Source: "container"}, nil
		}
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return Memory{}, fmt.Errorf("observe process memory: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "VmRSS:" {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return Memory{}, fmt.Errorf("observe process RSS: %w", err)
			}
			return Memory{Used: value * 1024, Source: "process"}, nil
		}
	}
	return Memory{}, fmt.Errorf("observe process RSS unavailable")
}
func parseQuota(data string) (*float64, error) {
	fields := strings.Fields(data)
	if len(fields) != 2 {
		return nil, fmt.Errorf("observe invalid CPU quota")
	}
	if fields[0] == "max" {
		return nil, nil
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("observe decode CPU quota: %w", err)
	}
	period, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("observe decode CPU period: %w", err)
	}
	if quota <= 0 || period <= 0 {
		return nil, nil
	}
	cores := float64(quota) / float64(period)
	return &cores, nil
}
func cpuUsage() (time.Duration, *float64, string, error) {
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		if cores, err := parseQuota(string(data)); err == nil && cores != nil {
			if stat, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
				for _, line := range strings.Split(string(stat), "\n") {
					fields := strings.Fields(line)
					if len(fields) == 2 && fields[0] == "usage_usec" {
						value, err := strconv.ParseInt(fields[1], 10, 64)
						if err == nil {
							return time.Duration(value) * time.Microsecond, cores, "container", nil
						}
					}
				}
			}
		}
	}
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, nil, "", fmt.Errorf("observe process CPU: %w", err)
	}
	var cores *float64
	for _, dir := range []string{"/sys/fs/cgroup/cpu", "/sys/fs/cgroup/cpu,cpuacct"} {
		quota, qerr := os.ReadFile(dir + "/cpu.cfs_quota_us")
		period, perr := os.ReadFile(dir + "/cpu.cfs_period_us")
		if qerr == nil && perr == nil {
			parsed, err := parseQuota(strings.TrimSpace(string(quota)) + " " + strings.TrimSpace(string(period)))
			if err == nil {
				cores = parsed
				break
			}
		}
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), cores, "process", nil
}
func diskSpace(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("observe filesystem usage: %w", err)
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}
