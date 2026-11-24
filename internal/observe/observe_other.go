//go:build !linux

package observe

import (
	"fmt"
	"time"
)

func memoryUsage() (Memory, error) {
	return Memory{}, fmt.Errorf("observe RSS unavailable on this platform")
}
func cpuUsage() (time.Duration, *float64, string, error) {
	return 0, nil, "", fmt.Errorf("observe CPU unavailable on this platform")
}
func diskSpace(string) (uint64, uint64, error) {
	return 0, 0, fmt.Errorf("observe filesystem usage unavailable on this platform")
}
