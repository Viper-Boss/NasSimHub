package modemmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
	"github.com/vishvananda/netlink"
)

// Vendor ECM fallback for modems that expose their data connection through a
// vendor-managed Ethernet data port instead of a ModemManager-owned netdev.
//
// Some LTE modules (Baiwang QDC507 and other UNISOC-family firmwares) report
// their connected bearer to ModemManager with the "ppp" IP method: MM dials
// with the generic AT plugin and offers a serial PPP data path. Raw PPP needs
// an LCP/IPCP lifecycle the ModemDeck data plane deliberately does not model.
// The same modems, however, also expose a CDC ECM control/data interface pair
// whose netdev is bridged into the module's own routing stack: once the PDP
// context is active, the netdev answers DHCP and forwards traffic over LTE.
//
// When ModemManager reports a PPP IPv4 method, this fallback rewrites the
// connection onto the sibling ECM netdev with a plain DHCP lease, so the
// regular netlink data plane (table/mark ownership, DHCP lifetime runtime)
// applies unchanged. The rewrite never touches non-PPP connections, and when
// no sibling netdev exists the connection is returned untouched so the caller
// surfaces the original, accurate PPP error.

// configureDataPlane applies the vendor ECM fallback, if any, before handing
// the connection to the data plane. Stale addresses on the ECM netdev are
// flushed once per acquisition: they can only originate from the modem's own
// DHCP server (a lease taken by the host or a previous run), and the data
// plane refuses to build on foreign addresses by design. Lines that already
// own network state are passed through without flushing so renewals never
// drop the applied lease.
func (p *Provider) configureDataPlane(
	ctx context.Context,
	lineID string,
	connection domain.DataConnection,
) error {
	const operation = "apply_device_configuration"
	rewritten, changed := ecmFallbackConnection(ecmNetdevForTTY, connection)
	if changed && !lineOwnedByDataPlane(p.dataPlane, lineID) {
		if err := flushIPv4Addresses(rewritten.Interface); err != nil {
			return domain.FailedPrecondition(
				operation,
				fmt.Sprintf(
					"the modem ECM data interface %q could not be prepared for a fresh lease",
					rewritten.Interface,
				),
				err,
			)
		}
	}
	return p.dataPlane.Configure(ctx, lineID, rewritten)
}

func lineOwnedByDataPlane(dataPlane DataPlane, lineID string) bool {
	for _, owned := range dataPlane.OwnedLines() {
		if owned == lineID {
			return true
		}
	}
	return false
}

// ecmFallbackConnection rewrites a PPP bearer onto the vendor ECM netdev.
// The resolver parameter keeps the sysfs walk out of unit tests.
func ecmFallbackConnection(
	resolver func(ttyName string) (string, error),
	connection domain.DataConnection,
) (domain.DataConnection, bool) {
	if !strings.EqualFold(strings.TrimSpace(connection.IPv4.Method), "ppp") {
		return connection, false
	}
	netdev, err := resolver(connection.Interface)
	if err != nil || strings.TrimSpace(netdev) == "" {
		return connection, false
	}
	rewritten := connection
	rewritten.Interface = netdev
	rewritten.IPv4.Method = "dhcp"
	rewritten.IPv4.Address = ""
	rewritten.IPv4.Prefix = 0
	rewritten.IPv4.Gateway = ""
	rewritten.IPv4.DNS = nil
	rewritten.IPv4.MTU = 0
	if strings.EqualFold(strings.TrimSpace(rewritten.IPv6.Method), "ppp") {
		rewritten.IPv6 = domain.IPConfiguration{}
	}
	return rewritten, true
}

// ecmNetdevForTTY finds the network interface that hangs off the same USB
// device as the modem's serial port, preferring the CDC ECM data port. The
// serial and the netdev share the USB device directory (for example
// /sys/devices/.../usb2/2-1), which is stable regardless of interface
// renaming (enx..., usb0, wwx...).
func ecmNetdevForTTY(ttyName string) (string, error) {
	name := strings.TrimSpace(ttyName)
	if name == "" {
		return "", errors.New("bearer reported no serial interface")
	}
	interfacePath, err := filepath.EvalSymlinks(
		filepath.Join("/sys/class/tty", name, "device"),
	)
	if err != nil {
		return "", fmt.Errorf("locate serial device %s in sysfs: %w", name, err)
	}
	usbDevice, err := usbDeviceDir(interfacePath)
	if err != nil {
		return "", fmt.Errorf("locate USB device for serial %s: %w", name, err)
	}
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return "", fmt.Errorf("list network interfaces in sysfs: %w", err)
	}
	fallback := ""
	for _, entry := range entries {
		devicePath, err := filepath.EvalSymlinks(
			filepath.Join("/sys/class/net", entry.Name(), "device"),
		)
		if err != nil {
			continue
		}
		netdevUSB, err := usbDeviceDir(devicePath)
		if err != nil || netdevUSB != usbDevice {
			continue
		}
		driver, err := os.Readlink(filepath.Join(devicePath, "driver"))
		if err == nil && filepath.Base(driver) == "cdc_ether" {
			return entry.Name(), nil
		}
		if fallback == "" {
			fallback = entry.Name()
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf(
		"no vendor ECM network interface shares USB device %s with %s",
		usbDevice,
		name,
	)
}

// usbDeviceDir walks a resolved sysfs path up to the USB device directory
// that owns it. sysfs symlinks differ per subsystem: a serial port resolves
// to .../<usb-device>/<interface>/<tty> while a netdev resolves straight to
// .../<usb-device>/<interface>. USB interfaces carry a colon in their name
// (for example 2-1:1.4), so the USB device is the parent of that component.
func usbDeviceDir(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		if strings.Contains(filepath.Base(current), ":") {
			return filepath.Dir(current), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no USB interface component in sysfs path %s", path)
		}
		current = parent
	}
}

// flushIPv4Addresses removes every IPv4 address from the ECM netdev. The
// netdev is exclusive to the modem, so any address on it comes from the
// modem's internal DHCP server rather than from an independent owner.
func flushIPv4Addresses(interfaceName string) error {
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return fmt.Errorf("find ECM interface %q: %w", interfaceName, err)
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("list addresses on %q: %w", interfaceName, err)
	}
	var result error
	for _, address := range addresses {
		if err := netlink.AddrDel(link, &address); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
