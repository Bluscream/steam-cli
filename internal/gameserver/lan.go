package gameserver

import (
	"net"
	"sort"
	"time"
)

// a2sInfoRequest is the A2S_INFO query: the simple-response header followed by
// the payload every Source-protocol server answers.
var a2sInfoRequest = append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x54}, append([]byte("Source Engine Query"), 0)...)

// DefaultLANPorts are the query ports worth broadcasting to. Most Source games
// use 27015-27020; the others cover common non-Source servers that still speak
// the protocol.
var DefaultLANPorts = []int{27015, 27016, 27017, 27018, 27019, 27020, 2303, 7777, 7778, 28015}

// DiscoverLAN finds servers on the local network by broadcasting A2S_INFO and
// collecting whoever answers.
//
// Any reply identifies a listening server, including the challenge that newer
// builds send instead of an immediate info response; the addresses are then
// queried properly so the caller gets full details. Steam's own LAN tab uses an
// internal protocol this cannot reach, so a server that ignores A2S will not
// appear here even though the client would list it.
func DiscoverLAN(ports []int, wait time.Duration) ([]string, error) {
	if len(ports) == 0 {
		ports = DefaultLANPorts
	}
	if wait <= 0 {
		wait = 2 * time.Second
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	targets := broadcastAddresses()
	for _, ip := range targets {
		for _, port := range ports {
			// A failed send to one broadcast domain should not stop the rest.
			_, _ = conn.WriteToUDP(a2sInfoRequest, &net.UDPAddr{IP: ip, Port: port})
		}
	}

	seen := map[string]bool{}
	buf := make([]byte, 1500)
	deadline := time.Now().Add(wait)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	for {
		_, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline reached
		}
		seen[from.String()] = true
		if time.Now().After(deadline) {
			break
		}
	}

	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out, nil
}

// broadcastAddresses returns the global broadcast plus each interface's own
// subnet broadcast, since some networks drop 255.255.255.255.
func broadcastAddresses() []net.IP {
	ips := []net.IP{net.IPv4bcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip := n.IP.To4()
			mask := net.IP(n.Mask).To4()
			if mask == nil {
				continue
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^mask[i]
			}
			ips = append(ips, b)
		}
	}
	return ips
}
