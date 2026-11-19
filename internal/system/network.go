// Package system wraps the host tools the web UI uses: NetworkManager,
// systemd and timedatectl. Privileges come from polkit rules in the NixOS
// module, so kioskd itself runs unprivileged.
package system

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", name, msg)
	}
	return string(out), nil
}

// splitTerse splits one line of `nmcli -t` output on unescaped colons.
func splitTerse(line string) []string {
	var fields []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			b.WriteByte(line[i])
		case c == ':':
			fields = append(fields, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(fields, b.String())
}

func nmcli(ctx context.Context, args ...string) ([][]string, error) {
	out, err := run(ctx, 30*time.Second, "nmcli", append([]string{"-t", "-e", "yes"}, args...)...)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			rows = append(rows, splitTerse(line))
		}
	}
	return rows, nil
}

type Interface struct {
	Device     string   `json:"device"`
	Type       string   `json:"type"`
	State      string   `json:"state"`
	Connection string   `json:"connection"`
	Method     string   `json:"method"` // auto or manual
	Addresses  []string `json:"addresses"`
	Gateway    string   `json:"gateway"`
	DNS        []string `json:"dns"`
}

func Interfaces(ctx context.Context) ([]Interface, error) {
	rows, err := nmcli(ctx, "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	if err != nil {
		return nil, err
	}
	var out []Interface
	for _, r := range rows {
		if len(r) < 4 || (r[1] != "ethernet" && r[1] != "wifi") {
			continue
		}
		it := Interface{Device: r[0], Type: r[1], State: r[2], Connection: r[3]}
		if dev, err := nmcli(ctx, "-f", "IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", it.Device); err == nil {
			for _, f := range dev {
				if len(f) < 2 || f[1] == "" {
					continue
				}
				switch {
				case strings.HasPrefix(f[0], "IP4.ADDRESS"):
					it.Addresses = append(it.Addresses, f[1])
				case f[0] == "IP4.GATEWAY":
					it.Gateway = f[1]
				case strings.HasPrefix(f[0], "IP4.DNS"):
					it.DNS = append(it.DNS, f[1])
				}
			}
		}
		if it.Connection != "" {
			if con, err := nmcli(ctx, "-f", "ipv4.method", "connection", "show", it.Connection); err == nil && len(con) > 0 && len(con[0]) > 1 {
				it.Method = con[0][1]
			}
		}
		out = append(out, it)
	}
	return out, nil
}

type WiFiNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"`
	Active   bool   `json:"active"`
}

func ScanWiFi(ctx context.Context) ([]WiFiNetwork, error) {
	rows, err := nmcli(ctx, "-f", "ACTIVE,SSID,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	best := map[string]WiFiNetwork{}
	var order []string
	for _, r := range rows {
		if len(r) < 4 || r[1] == "" {
			continue
		}
		sig, _ := strconv.Atoi(r[2])
		n := WiFiNetwork{SSID: r[1], Signal: sig, Security: r[3], Active: r[0] == "yes"}
		cur, seen := best[n.SSID]
		if !seen {
			order = append(order, n.SSID)
		}
		if !seen || n.Signal > cur.Signal || n.Active {
			best[n.SSID] = n
		}
	}
	out := make([]WiFiNetwork, 0, len(order))
	for _, s := range order {
		out = append(out, best[s])
	}
	return out, nil
}

func ConnectWiFi(ctx context.Context, ssid, password string) error {
	if ssid == "" {
		return errors.New("SSID is required")
	}
	args := []string{"device", "wifi", "connect", ssid}
	if password != "" {
		args = append(args, "password", password)
	}
	_, err := run(ctx, 60*time.Second, "nmcli", args...)
	return err
}

type IPv4Settings struct {
	Connection string   `json:"connection"`
	Method     string   `json:"method"`  // auto or manual
	Address    string   `json:"address"` // CIDR, manual only
	Gateway    string   `json:"gateway"`
	DNS        []string `json:"dns"`
}

func SetIPv4(ctx context.Context, s IPv4Settings) error {
	if s.Connection == "" {
		return errors.New("connection is required")
	}
	args := []string{"connection", "modify", s.Connection}
	switch s.Method {
	case "auto":
		args = append(args, "ipv4.method", "auto", "ipv4.addresses", "", "ipv4.gateway", "")
	case "manual":
		if _, err := netip.ParsePrefix(s.Address); err != nil {
			return fmt.Errorf("address must be CIDR like 192.168.1.50/24: %w", err)
		}
		if s.Gateway != "" {
			if _, err := netip.ParseAddr(s.Gateway); err != nil {
				return fmt.Errorf("invalid gateway: %w", err)
			}
		}
		args = append(args, "ipv4.method", "manual", "ipv4.addresses", s.Address, "ipv4.gateway", s.Gateway)
	default:
		return errors.New("method must be auto or manual")
	}
	for _, d := range s.DNS {
		if _, err := netip.ParseAddr(d); err != nil {
			return fmt.Errorf("invalid DNS server %q", d)
		}
	}
	args = append(args, "ipv4.dns", strings.Join(s.DNS, ","), "ipv4.ignore-auto-dns", strconv.FormatBool(len(s.DNS) > 0))
	if _, err := run(ctx, 30*time.Second, "nmcli", args...); err != nil {
		return err
	}
	_, err := run(ctx, 60*time.Second, "nmcli", "connection", "up", s.Connection)
	return err
}
