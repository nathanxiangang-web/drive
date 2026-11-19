package status

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type Capacity struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
}

type Server struct {
	OnlineSeconds int64     `json:"online_seconds"`
	AppSeconds    int64     `json:"app_seconds"`
	CPUs          int       `json:"cpus"`
	Load          string    `json:"load,omitempty"`
	Memory        *Capacity `json:"memory,omitempty"`
	Disk          *Capacity `json:"disk,omitempty"`
}

func Read(started time.Time) Server {
	result := Server{AppSeconds: int64(time.Since(started) / time.Second), CPUs: runtime.NumCPU()}
	if raw, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(raw)); len(fields) > 0 {
			seconds, _ := strconv.ParseFloat(fields[0], 64)
			result.OnlineSeconds = int64(seconds)
		}
	}
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(raw)); len(fields) >= 3 {
			result.Load = strings.Join(fields[:3], " / ")
		}
	}
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, available uint64
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[2] != "kB" {
				continue
			}
			switch fields[0] {
			case "MemTotal:":
				total, _ = strconv.ParseUint(fields[1], 10, 64)
			case "MemAvailable:":
				available, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}
		if total > 0 {
			result.Memory = &Capacity{Total: total * 1024, Available: available * 1024}
		}
	}
	var stat unix.Statfs_t
	if unix.Statfs("/", &stat) == nil && stat.Blocks > 0 && stat.Bsize > 0 {
		result.Disk = &Capacity{Total: stat.Blocks * uint64(stat.Bsize), Available: stat.Bavail * uint64(stat.Bsize)}
	}
	return result
}
