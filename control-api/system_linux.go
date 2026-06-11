//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// systemMetrics reads cpu/mem/disk/load/uptime from /proc and statfs. Linux
// only; the !linux build returns zeros (dev machines).

func systemMetrics() sysMetrics {
	var m sysMetrics
	c1, t1, ok1 := cpuSample()
	if ok1 {
		time.Sleep(200 * time.Millisecond)
		c2, t2, ok2 := cpuSample()
		if ok2 && t2 > t1 {
			busy := float64((t2 - t1) - (c2 - c1))
			m.CPUPercent = 100 * busy / float64(t2-t1)
		}
	}
	m.MemPercent = memPercent()
	m.DiskPercent = diskPercent("/")
	m.Load1 = loadOne()
	m.UptimeSec = uptimeSeconds()
	return m
}

// cpuSample returns (idle, total, ok) jiffies from /proc/stat.
func cpuSample() (uint64, uint64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		var total, idle uint64
		for i, f := range fields {
			v, _ := strconv.ParseUint(f, 10, 64)
			total += v
			if i == 3 { // idle
				idle = v
			}
		}
		return idle, total, true
	}
	return 0, 0, false
}

func memPercent() float64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, avail float64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(f[1], 64)
		switch f[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			avail = v
		}
	}
	if total == 0 {
		return 0
	}
	return 100 * (1 - avail/total)
}

func diskPercent(path string) float64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil || st.Blocks == 0 {
		return 0
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	free := float64(st.Bavail) * float64(st.Bsize)
	return 100 * (1 - free/total)
}

func loadOne() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func uptimeSeconds() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}
