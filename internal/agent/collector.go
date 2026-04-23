package agent

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// MetricsSnapshot holds a point-in-time metrics reading from any agent type
type MetricsSnapshot struct {
	CPUPct         float64
	MemoryPct      float64
	RequestsPerSec float64
	LatencyP99Ms   float64
	ErrorRatePct   float64
	ReplicaCount   int32
	Timestamp      time.Time
}

// ProcCollector collects metrics from /proc on Linux hosts (for VM/bare-metal agents)
type ProcCollector struct {
	prevCPUIdle  uint64
	prevCPUTotal uint64
}

func NewProcCollector() *ProcCollector {
	return &ProcCollector{}
}

// CollectCPU reads CPU utilization from /proc/stat
func (c *ProcCollector) CollectCPU() (float64, error) {
	if runtime.GOOS != "linux" {
		return 0, fmt.Errorf("proc collector only supported on linux")
	}

	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, fmt.Errorf("failed to read /proc/stat")
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, fmt.Errorf("unexpected /proc/stat format")
	}

	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		total += v
		if i == 4 { // idle is the 4th field after "cpu"
			idle = v
		}
	}

	// Compute delta since last reading
	deltaTotal := total - c.prevCPUTotal
	deltaIdle := idle - c.prevCPUIdle
	c.prevCPUTotal = total
	c.prevCPUIdle = idle

	if deltaTotal == 0 {
		return 0, nil
	}

	cpuPct := (1.0 - float64(deltaIdle)/float64(deltaTotal)) * 100.0
	return cpuPct, nil
}

// CollectMemory reads memory utilization from /proc/meminfo
func (c *ProcCollector) CollectMemory() (float64, error) {
	if runtime.GOOS != "linux" {
		return 0, fmt.Errorf("proc collector only supported on linux")
	}

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var memTotal, memAvailable uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotal, _ = strconv.ParseUint(fields[1], 10, 64)
		case "MemAvailable:":
			memAvailable, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}

	if memTotal == 0 {
		return 0, nil
	}

	memUsedPct := (1.0 - float64(memAvailable)/float64(memTotal)) * 100.0
	return memUsedPct, nil
}

// Collect returns a full snapshot using /proc
func (c *ProcCollector) Collect() MetricsSnapshot {
	cpu, _ := c.CollectCPU()
	mem, _ := c.CollectMemory()
	return MetricsSnapshot{
		CPUPct:    cpu,
		MemoryPct: mem,
		Timestamp: time.Now(),
	}
}

// RuntimeCollector collects Go runtime metrics (fallback for non-Linux or testing)
type RuntimeCollector struct{}

func NewRuntimeCollector() *RuntimeCollector {
	return &RuntimeCollector{}
}

func (c *RuntimeCollector) Collect() MetricsSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Use Go runtime stats as a proxy
	memPct := float64(m.Alloc) / float64(m.Sys) * 100
	cpuPct := float64(runtime.NumGoroutine()) / 100.0 * 10 // rough proxy

	return MetricsSnapshot{
		CPUPct:    cpuPct,
		MemoryPct: memPct,
		Timestamp: time.Now(),
	}
}
