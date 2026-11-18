package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/rumblefrog/go-a2s"
)

const queryTimeout = 3 * time.Second

type Status struct {
	Name, Map          string
	Players, Bots, Max int
}

// Humans is the player count without bots (A2S counts bots as players).
func (s *Status) Humans() int { return max(s.Players-s.Bots, 0) }

func queryInfo(addr string) (*Status, error) {
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(queryTimeout))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	info, err := c.QueryInfo()
	if err != nil {
		return nil, err
	}
	return &Status{Name: info.Name, Map: info.Map, Players: int(info.Players), Bots: int(info.Bots), Max: int(info.MaxPlayers)}, nil
}

func queryPlayers(addr string) ([]*a2s.Player, error) {
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(queryTimeout))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	p, err := c.QueryPlayer()
	if err != nil {
		return nil, err
	}
	return p.Players, nil
}

var errBadAddress = errors.New("bad address")

// resolveAddr turns user input ("host" or "host:port") into "ip:port". It refuses
// non-public IPs so users can't make the bot send packets into the host's own network,
// and stores the IP (not the name) so a DNS change can't redirect it there later.
func resolveAddr(input string) (string, error) {
	input = strings.TrimSpace(input)
	host, port, err := net.SplitHostPort(input)
	if err != nil {
		host, port = strings.Trim(input, "[]"), "27015"
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 || host == "" {
		return "", errBadAddress
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", errBadAddress
	}
	ip := ips[0]
	for _, cand := range ips { // prefer IPv4: most CS2 servers only listen there
		if cand.To4() != nil {
			ip = cand
			break
		}
	}
	if !isPublicIP(ip) {
		return "", errBadAddress
	}
	return net.JoinHostPort(ip.String(), port), nil
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || cgnat.Contains(ip))
}

// runQueryCLI is `shiron query <ip:port>`: checks a CS2 server without Discord.
func runQueryCLI(addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "27015")
	}
	st, err := queryInfo(addr)
	if err != nil {
		return fmt.Errorf("info query failed: %w", err)
	}
	fmt.Printf("Name:    %s\nMap:     %s\nPlayers: %d/%d (%d bots)\n", st.Name, st.Map, st.Humans(), st.Max, st.Bots)
	players, err := queryPlayers(addr)
	if err != nil {
		fmt.Println("Player list: query failed:", err)
		return nil
	}
	fmt.Printf("Player list (%d entries):\n", len(players))
	for _, p := range players {
		fmt.Printf("  %-32s score %-4d %s\n", p.Name, p.Score, time.Duration(p.Duration)*time.Second)
	}
	return nil
}
