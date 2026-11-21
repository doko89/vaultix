package agent

import (
	"net"
	"runtime"
)

// HostFacts are outbound-facing host facts reported with heartbeats.
// Empty IP and zero disk values mean "unknown" and are omitted from the
// heartbeat payload (the manager keeps its stored values).
type HostFacts struct {
	OS   string
	Arch string
	IP   string
	// DiskTotal and DiskFree cover the filesystem holding DiskProbePath.
	DiskTotal uint64
	DiskFree  uint64
}

// DiskProbePath is the filesystem heartbeat disk facts describe: the root
// volume, which is what an operator means by "agent disk".
const DiskProbePath = "/"

// CollectHostFacts gathers reporting facts: GOOS/GOARCH, the first
// non-loopback IPv4, and root-volume disk totals. Every probe fails open
// to its zero value — a heartbeat must never fail for lack of sysinfo.
func CollectHostFacts() HostFacts {
	facts := HostFacts{OS: runtime.GOOS, Arch: runtime.GOARCH}
	facts.IP, _ = outboundIPv4()
	if total, free, err := diskUsage(DiskProbePath); err == nil {
		facts.DiskTotal, facts.DiskFree = total, free
	}
	return facts
}

// outboundIPv4 returns the first non-loopback, up interface's IPv4 — the
// address peers most likely see. "" when none qualifies.
func outboundIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String(), nil
			}
		}
	}
	return "", nil
}
