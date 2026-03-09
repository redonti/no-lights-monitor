package ping

import (
	"log"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// PingHost sends ICMP pings to the target and returns true if reachable.
func PingHost(target string) bool {
	pinger, err := probing.NewPinger(target)
	if err != nil {
		log.Printf("[ping] failed to create pinger for %s: %v", target, err)
		return false
	}
	pinger.Count = 3
	pinger.Timeout = 5 * time.Second
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		return false
	}
	return pinger.Statistics().PacketsRecv > 0
}
