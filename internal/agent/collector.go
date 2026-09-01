package agent

import (
	"runtime"
	"time"

	"github.com/larry-probe/larry/internal/protocol"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// Collector gathers a single system metric sample. It is constructed once
// and keeps the previous network byte counters so it can report per-second
// throughput rather than cumulative totals.
type Collector struct {
	info       *host.InfoStat
	prevNetIn  uint64
	prevNetOut uint64
	prevTS     time.Time
}

// NewCollector performs the one-time host-info lookup (kernel, platform, uptime
// baseline) so each Collect is cheap. It also seeds the network counters.
func NewCollector() (*Collector, error) {
	info, err := host.Info()
	if err != nil {
		return nil, err
	}
	c := &Collector{info: info}
	// seed network counters so the first report does not show a huge spike
	if io, err := net.IOCounters(false); err == nil && len(io) > 0 {
		c.prevNetIn = io[0].BytesRecv
		c.prevNetOut = io[0].BytesSent
	}
	c.prevTS = time.Now()
	return c, nil
}

// HostInfo returns cached host metadata for the Hello message.
func (c *Collector) HostInfo() (hostname, kernel string) {
	if c.info == nil {
		return "", ""
	}
	return c.info.Hostname, c.info.KernelVersion
}

// Uptime returns seconds since boot.
func (c *Collector) Uptime() uint64 {
	if c.info == nil {
		return 0
	}
	return uint64(time.Since(time.Unix(int64(c.info.BootTime), 0)) / time.Second)
}

// BootTime returns the unix boot time.
func (c *Collector) BootTime() int64 {
	if c.info == nil {
		return 0
	}
	return int64(c.info.BootTime)
}

// Collect takes one metric sample. CPU usage needs a sampling window, so we
// use a short 500ms blocking measurement — accurate enough and keeps the
// collector stateless about timing.
func (c *Collector) Collect() (protocol.Report, error) {
	var r protocol.Report
	r.TS = protocol.Now()

	// CPU: percent over a 500ms window (first arg false = aggregate all cores).
	pct, err := cpu.Percent(500*time.Millisecond, false)
	if err == nil && len(pct) > 0 {
		r.CPU = round1(pct[0])
	}

	// Memory + swap.
	if vm, err := mem.VirtualMemory(); err == nil {
		r.MemUsed = vm.Used
		r.MemTotal = vm.Total
	}
	if sm, err := mem.SwapMemory(); err == nil {
		r.SwapUsed = sm.Used
		r.SwapTotal = sm.Total
	}

	// Disks: per-mount usage of real filesystems only.
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if skipMount(p.Mountpoint) {
				continue
			}
			u, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
			r.Disks = append(r.Disks, protocol.Disk{
				Mount: p.Mountpoint,
				Total: u.Total,
				Used:  u.Used,
			})
		}
	}

	// Network throughput: bytes/s since the previous sample.
	if io, err := net.IOCounters(false); err == nil && len(io) > 0 {
		now := time.Now()
		dt := now.Sub(c.prevTS).Seconds()
		if dt > 0 {
			r.NetIn = uint64(float64(io[0].BytesRecv-c.prevNetIn) / dt)
			r.NetOut = uint64(float64(io[0].BytesSent-c.prevNetOut) / dt)
		}
		c.prevNetIn = io[0].BytesRecv
		c.prevNetOut = io[0].BytesSent
		c.prevTS = now
	}

	// Load average (Linux/Darwin; Windows yields zeros via gopsutil).
	if lv, err := load.Avg(); err == nil {
		r.Load1 = round1(lv.Load1)
		r.Load5 = round1(lv.Load5)
		r.Load15 = round1(lv.Load15)
	}

	// Established TCP connection count.
	if conns, err := net.Connections("tcp"); err == nil {
		for _, cn := range conns {
			if cn.Status == "ESTABLISHED" {
				r.Conns++
			}
		}
	}

	// Process count.
	if procs, err := process.Processes(); err == nil {
		r.Procs = uint64(len(procs))
	}

	r.Uptime = c.Uptime()
	r.BootTime = c.BootTime()

	// Temperatures (best effort — not all platforms expose sensors).
	if temps, err := host.SensorsTemperatures(); err == nil {
		m := make(map[string]float64, len(temps))
		for _, t := range temps {
			if t.Temperature > 0 {
				m[t.SensorKey] = round1(t.Temperature)
			}
		}
		if len(m) > 0 {
			r.Temps = m
		}
	}

	return r, nil
}

// skipMount hides pseudo/virtual filesystems that are not real storage.
func skipMount(m string) bool {
	switch m {
	case "", "/", "/boot", "/boot/efi", "/dev", "/dev/shm":
		return false
	}
	// skip common virtual filesystems
	for _, p := range []string{"/proc", "/sys", "/run", "/snap", "/var/lib/docker"} {
		if m == p || hasPrefix(m, p+"/") {
			return true
		}
	}
	return false
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

// RuntimeInfo returns GOOS/GOARCH for the Hello message.
func RuntimeInfo() (os, arch string) {
	return runtime.GOOS, runtime.GOARCH
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
