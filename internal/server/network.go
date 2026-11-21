package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"darkserve/internal/auth"
)

// outboundIP finds the address this machine would use to reach the local
// network. It opens a UDP socket (no traffic is sent) and reads back the
// chosen source address.
func outboundIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

// lanAddresses lists every non-loopback IPv4 on this machine.
func lanAddresses() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				out = append(out, v4.String())
			}
		}
	}
	return out
}

// GET /api/network — the address a TV on the same network should use to reach
// this server. Casting needs a real LAN address, never "localhost".
func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	addr := strings.TrimSpace(s.cfg.Addr)
	port := "7676"
	if strings.HasPrefix(addr, ":") {
		port = strings.TrimPrefix(addr, ":")
	} else if i := strings.LastIndex(addr, ":"); i >= 0 {
		port = addr[i+1:]
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}

	ip := outboundIP()
	base := "http://" + ip + ":" + port
	if ip == "" {
		base = ""
	}
	// An override wins when the machine has several networks or the port is
	// forwarded to a different address.
	if override := strings.TrimSpace(s.cfg.CastURL); override != "" {
		base = strings.TrimRight(override, "/")
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"base_url":  base,
		"port":      port,
		"addresses": lanAddresses(),
		"override":  strings.TrimSpace(s.cfg.CastURL),
	})
}
