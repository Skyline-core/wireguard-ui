package util

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/gommon/log"
)

func ensureWgQuickAllowed(confPath string) (string, error) {
	confPath = strings.TrimSpace(confPath)
	if confPath == "" {
		return "", fmt.Errorf("missing config file path")
	}
	if !filepath.IsAbs(confPath) {
		return "", fmt.Errorf("config path must be absolute")
	}
	if !LookupEnvOrBool(AllowWgQuickCtlEnvVar, false) {
		return "", fmt.Errorf("wg-quick controls disabled (set %s=true)", AllowWgQuickCtlEnvVar)
	}
	return confPath, nil
}

// RunWgQuickDown runs `wg-quick down <config>` only when WGUI_ALLOW_WG_QUICK is enabled (security gate).
func RunWgQuickDown(confPath string) error {
	p, err := ensureWgQuickAllowed(confPath)
	if err != nil {
		return err
	}
	out, err := exec.Command("wg-quick", "down", p).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick down: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunWgQuickUp runs `wg-quick up <config>` only when WGUI_ALLOW_WG_QUICK is enabled (security gate).
func RunWgQuickUp(confPath string) error {
	p, err := ensureWgQuickAllowed(confPath)
	if err != nil {
		return err
	}
	out, err := exec.Command("wg-quick", "up", p).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick up: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// wgQuickSystemdUnitName returns e.g. "wg-quick@wg0", matching journalctl/systemctl `-u wg-quick@iface`.
func wgQuickSystemdUnitName(confPath string) string {
	iface := WireGuardIfaceBasename(confPath)
	if strings.TrimSpace(iface) == "" {
		iface = "wg0"
	}
	return "wg-quick@" + iface
}

func systemdShowLoadState(unit string) (string, error) {
	out, err := exec.Command("systemctl", "show", unit, "-p", "LoadState", "--value").CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RestartWgQuick runs `wg-quick down` + `wg-quick up` unless a matching systemd unit is active (see WGUI_WG_RESTART_VIA_SYSTEMD),
// in which case `systemctl restart wg-quick@iface` is used so journalctl -u wg-quick@iface captures the event.
func RestartWgQuick(confPath string) error {
	p, err := ensureWgQuickAllowed(confPath)
	if err != nil {
		return err
	}

	if runtime.GOOS == "linux" && LookupEnvOrBool(RestartWGViaSystemdEnvVar, true) {
		if _, lpErr := exec.LookPath("systemctl"); lpErr == nil {
			unit := wgQuickSystemdUnitName(p)
			loadState, lsErr := systemdShowLoadState(unit)
			if lsErr == nil && loadState == "loaded" {
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer cancel()
				out, rstErr := exec.CommandContext(ctx, "systemctl", "restart", unit).CombinedOutput()
				if rstErr == nil {
					log.Infof("WireGuard restart: systemctl restart %s", unit)
					return nil
				}
				log.Warnf("systemctl restart %s failed (%v), falling back to wg-quick: %s", unit, rstErr, strings.TrimSpace(string(out)))
			}
		}
	}

	_ = RunWgQuickDown(p)
	return RunWgQuickUp(p)
}

