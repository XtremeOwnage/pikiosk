package system

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Unit names come from the NixOS module.
const (
	UpgradeUnit  = "pikiosk-upgrade.service"
	RollbackUnit = "pikiosk-rollback.service"
)

func Reboot(ctx context.Context) error {
	_, err := run(ctx, 15*time.Second, "systemctl", "reboot")
	return err
}

func StartUnit(ctx context.Context, unit string) error {
	_, err := run(ctx, 15*time.Second, "systemctl", "start", "--no-block", unit)
	return err
}

type UnitStatus struct {
	ActiveState string `json:"active_state"`
	Result      string `json:"result"`
	Log         string `json:"log"`
}

func UnitInfo(ctx context.Context, unit string) (UnitStatus, error) {
	out, err := run(ctx, 10*time.Second, "systemctl", "show", "-p", "ActiveState", "-p", "Result", unit)
	if err != nil {
		return UnitStatus{}, err
	}
	var st UnitStatus
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "ActiveState":
			st.ActiveState = v
		case "Result":
			st.Result = v
		}
	}
	st.Log, _ = run(ctx, 10*time.Second, "journalctl", "-u", unit, "-n", "60", "--no-pager", "-o", "cat")
	return st, nil
}

func SetTimezone(ctx context.Context, tz string) error {
	if tz == "" || strings.ContainsAny(tz, " ;&|") {
		return errors.New("invalid timezone")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return err
	}
	_, err := run(ctx, 15*time.Second, "timedatectl", "set-timezone", tz)
	return err
}

type Info struct {
	Hostname   string    `json:"hostname"`
	OSVersion  string    `json:"os_version"`
	Version    string    `json:"version"`
	Timezone   string    `json:"timezone"`
	Time       time.Time `json:"time"`
	Uptime     string    `json:"uptime"`
	Addresses  []string  `json:"addresses"`
	Generation string    `json:"generation"`
	Model      string    `json:"model,omitempty"`
	// Rollback is true on NixOS, where the previous generation can be booted.
	Rollback bool `json:"rollback"`
}

func GetInfo(version string) Info {
	host, _ := os.Hostname()
	info := Info{Hostname: host, Version: version, Time: time.Now(), Timezone: time.Now().Location().String()}
	if b, err := os.ReadFile("/run/current-system/nixos-version"); err == nil {
		info.OSVersion = "NixOS " + strings.TrimSpace(string(b))
		info.Rollback = true
	} else if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				info.OSVersion = strings.Trim(v, `"`)
			}
		}
	}
	if b, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		info.Model = strings.TrimRight(string(b), "\x00\n")
	}
	if link, err := os.Readlink("/nix/var/nix/profiles/system"); err == nil {
		info.Generation = link
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		info.Timezone = strings.TrimSpace(string(b))
	} else if link, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(link, "zoneinfo/"); i >= 0 {
			info.Timezone = link[i+len("zoneinfo/"):]
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if d, err := time.ParseDuration(f[0] + "s"); err == nil {
				info.Uptime = d.Truncate(time.Minute).String()
			}
		}
	}
	info.Addresses = LocalAddresses()
	return info
}

func LocalAddresses() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// Stats is device health for MQTT and the web UI.
func Stats() map[string]any {
	st := map[string]any{}
	if b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp"); err == nil {
		var milli int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &milli); err == nil {
			st["cpu_temp_c"] = float64(milli/100) / 10
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		var up float64
		if _, err := fmt.Sscanf(string(b), "%f", &up); err == nil {
			st["boot_time"] = time.Now().Add(-time.Duration(up * float64(time.Second))).UTC().Truncate(time.Second).Format(time.RFC3339)
		}
	}
	if addrs := LocalAddresses(); len(addrs) > 0 {
		st["ip"] = addrs[0]
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		var l1 float64
		if _, err := fmt.Sscanf(string(b), "%f", &l1); err == nil {
			st["load"] = l1
		}
	}
	return st
}
