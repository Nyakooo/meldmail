// Package observe samples lightweight process/container counters on demand.
package observe

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type Memory struct {
	Used   uint64  `json:"used_bytes"`
	Limit  *uint64 `json:"limit_bytes"`
	Source string  `json:"source"`
}
type CPU struct {
	Percent  *float64 `json:"percent"`
	Cores    *float64 `json:"limit_cores"`
	Source   string   `json:"source"`
	Interval float64  `json:"interval_seconds"`
}
type Disk struct {
	Database        uint64 `json:"database_bytes"`
	WAL             uint64 `json:"wal_bytes"`
	SHM             uint64 `json:"shm_bytes"`
	Total           uint64 `json:"data_bytes"`
	Available       bool   `json:"available"`
	VolumeTotal     uint64 `json:"volume_total_bytes"`
	VolumeAvailable uint64 `json:"volume_available_bytes"`
	VolumeKnown     bool   `json:"volume_known"`
}
type Snapshot struct {
	Memory    Memory `json:"memory"`
	CPU       CPU    `json:"cpu"`
	Disk      Disk   `json:"disk"`
	Timestamp int64  `json:"timestamp"`
}
type Collector struct {
	mu          sync.Mutex
	previous    time.Time
	previousCPU time.Duration
	source      string
	cpu         CPU
}

func New() *Collector { return &Collector{} }
func (c *Collector) Snapshot(database string) Snapshot {
	memory, err := memoryUsage()
	if err != nil {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		memory = Memory{Used: m.Sys, Source: "go_runtime"}
	}
	c.mu.Lock()
	now := time.Now()
	usage, cores, source, err := cpuUsage()
	if err == nil {
		elapsed := now.Sub(c.previous)
		if c.source != source || c.previous.IsZero() || usage < c.previousCPU {
			c.previous = now
			c.previousCPU = usage
			c.source = source
			c.cpu = CPU{Cores: cores, Source: source}
		} else if elapsed >= time.Second {
			percent := float64(usage-c.previousCPU) / float64(elapsed) * 100
			if cores != nil && *cores > 0 {
				percent /= *cores
			}
			c.cpu = CPU{Percent: &percent, Cores: cores, Source: source, Interval: elapsed.Seconds()}
			c.previous = now
			c.previousCPU = usage
		}
	} else {
		c.cpu = CPU{Source: "unavailable"}
	}
	cpu := c.cpu
	c.mu.Unlock()
	disk := Disk{Available: database != ""}
	if database != "" {
		for _, file := range []struct {
			suffix string
			target *uint64
		}{{"", &disk.Database}, {"-wal", &disk.WAL}, {"-shm", &disk.SHM}} {
			info, err := os.Stat(database + file.suffix)
			if err != nil {
				if file.suffix == "" || !os.IsNotExist(err) {
					disk.Available = false
				}
				continue
			}
			if info.Size() > 0 {
				*file.target = uint64(info.Size())
			}
			disk.Total += *file.target
		}
		total, available, err := diskSpace(filepath.Dir(database))
		if err == nil {
			disk.VolumeTotal = total
			disk.VolumeAvailable = available
			disk.VolumeKnown = true
		}
	}
	return Snapshot{Memory: memory, CPU: cpu, Disk: disk, Timestamp: now.Unix()}
}
