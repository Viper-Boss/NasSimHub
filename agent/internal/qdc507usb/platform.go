package qdc507usb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

const (
	defaultSysfsRoot  = "/sys"
	defaultDeviceRoot = "/dev"
)

type sysfsDeviceAccess struct {
	sysfsRoot string
	devRoot   string
	poll      time.Duration
}

func newSysfsDeviceAccess() *sysfsDeviceAccess {
	return &sysfsDeviceAccess{
		sysfsRoot: defaultSysfsRoot,
		devRoot:   defaultDeviceRoot,
		poll:      100 * time.Millisecond,
	}
}

func (a *sysfsDeviceAccess) Inspect(physicalDevice string) (USBInterfaces, error) {
	physicalDevice, err := cleanPhysicalDevice(physicalDevice)
	if err != nil {
		return USBInterfaces{}, err
	}
	vendorID, err := readHexAttribute(filepath.Join(physicalDevice, "idVendor"))
	if err != nil {
		return USBInterfaces{}, fmt.Errorf("read USB vendor ID: %w", err)
	}
	productID, err := readHexAttribute(filepath.Join(physicalDevice, "idProduct"))
	if err != nil {
		return USBInterfaces{}, fmt.Errorf("read USB product ID: %w", err)
	}
	interfaces := USBInterfaces{VendorID: vendorID, ProductID: productID}
	entries, err := os.ReadDir(physicalDevice)
	if err != nil {
		return USBInterfaces{}, err
	}
	prefix := filepath.Base(physicalDevice) + ":"
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		interfacePath := filepath.Join(physicalDevice, entry.Name())
		class, classErr := readHexAttribute(filepath.Join(interfacePath, "bInterfaceClass"))
		subclass, subclassErr := readHexAttribute(filepath.Join(interfacePath, "bInterfaceSubClass"))
		protocol, protocolErr := readHexAttribute(filepath.Join(interfacePath, "bInterfaceProtocol"))
		if classErr != nil || subclassErr != nil || protocolErr != nil {
			continue
		}
		if class == 0xff && subclass == 0x42 && protocol == 0x01 {
			interfaces.ADB = true
			interfaces.ADBDriver = readInterfaceDriver(interfacePath)
		}
		if class == 0x01 && subclass == 0x01 {
			interfaces.UACControl = true
			interfaces.UACDriver = readInterfaceDriver(interfacePath)
		}
		if class == 0x01 && subclass == 0x02 {
			interfaces.UACStreaming = true
			// The streaming interfaces (1.8/1.9) are distinct from the UAC
			// control interface (1.7): the option driver can claim either set
			// independently, so a hijack that only covers the streams must not
			// hide behind a correctly bound control interface.
			if driver := readInterfaceDriver(interfacePath); driver != "" &&
				interfaces.UACStreamingDriver == "" {
				interfaces.UACStreamingDriver = driver
			}
		}
	}
	return interfaces, nil
}

// readInterfaceDriver returns the kernel driver bound to a USB interface. The
// driver is exposed as a "driver" symlink under the interface sysfs directory.
// When the kernel has not bound any driver the symlink is absent, which is not
// an error: we return an empty string and let callers treat that as "no
// driver" rather than as a hardware fault.
func readInterfaceDriver(interfacePath string) string {
	link := filepath.Join(interfacePath, "driver")
	target, err := os.Readlink(link)
	if err != nil {
		// No driver symlink (or unreadable): treat as no driver bound.
		return ""
	}
	return filepath.Base(target)
}

// driverHijacked reports whether a non-serial QDC507 interface was claimed by
// the kernel's generic serial driver instead of its functional driver. The
// descriptors are still correct in that state, so voiceReady() alone cannot
// see it: the ADB interface belongs to usbfs and the UAC interfaces to
// snd-usb-audio, but the option driver has taken them over. The UAC control
// (1.7) and streaming (1.8/1.9) interfaces are checked separately because the
// option driver can claim only one of the two sets.
func (u USBInterfaces) driverHijacked() bool {
	if u.ADB && u.ADBDriver != "" && u.ADBDriver != "usbfs" && u.ADBDriver != "usb" {
		return true
	}
	if u.UACControl && u.UACDriver != "" && u.UACDriver != "snd-usb-audio" && u.UACDriver != "usb" {
		return true
	}
	if u.UACStreaming && u.UACStreamingDriver != "" &&
		u.UACStreamingDriver != "snd-usb-audio" && u.UACStreamingDriver != "usb" {
		return true
	}
	return false
}

func (a *sysfsDeviceAccess) ATPort(line domain.Line) (ATPort, error) {
	physicalDevice, err := cleanPhysicalDevice(line.PhysicalDevice)
	if err != nil {
		return ATPort{}, err
	}
	type candidate struct {
		port ATPort
		key  string
	}
	var candidates []candidate
	for _, modemPort := range line.Ports {
		if !strings.EqualFold(strings.TrimSpace(modemPort.Type), "at") {
			continue
		}
		name := strings.TrimSpace(modemPort.Name)
		if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, "/\\\x00") {
			continue
		}
		deviceLink := filepath.Join(a.sysfsRoot, "class", "tty", name, "device")
		resolved, err := filepath.EvalSymlinks(deviceLink)
		if err != nil {
			continue
		}
		resolved = filepath.Clean(resolved)
		if resolved != physicalDevice && !strings.HasPrefix(resolved, physicalDevice+string(filepath.Separator)) {
			continue
		}
		relative, err := filepath.Rel(physicalDevice, resolved)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}
		interfaceName := strings.Split(relative, string(filepath.Separator))[0]
		if !strings.Contains(interfaceName, ":") {
			continue
		}
		candidates = append(candidates, candidate{
			key: interfaceName + "\x00" + name,
			port: ATPort{
				Device:          filepath.Join(a.devRoot, name),
				Interface:       interfaceName,
				SysfsDevicePath: resolved,
			},
		})
	}
	if len(candidates) == 0 {
		return ATPort{}, fmt.Errorf("line has no AT port under its physical USB device")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].key < candidates[j].key })
	return candidates[0].port, nil
}

func (a *sysfsDeviceAccess) WaitForReenumeration(
	ctx context.Context,
	physicalDevice string,
	previous ATPort,
	vendorID int,
	productID int,
	timeout time.Duration,
) (ATPort, USBInterfaces, error) {
	physicalDevice, err := cleanPhysicalDevice(physicalDevice)
	if err != nil {
		return ATPort{}, USBInterfaces{}, err
	}
	if previous.Interface == "" || previous.Interface != filepath.Base(previous.Interface) {
		return ATPort{}, USBInterfaces{}, fmt.Errorf("previous AT interface is invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	poll := a.poll
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}

	detached := false
	for !detached {
		if _, err := os.Stat(previous.SysfsDevicePath); os.IsNotExist(err) {
			detached = true
			break
		}
		select {
		case <-bounded.Done():
			return ATPort{}, USBInterfaces{}, fmt.Errorf("QDC507 did not disconnect: %w", bounded.Err())
		case <-time.After(poll):
		}
	}

	var lastErr error
	for {
		interfaces, inspectErr := a.Inspect(physicalDevice)
		if inspectErr == nil {
			if interfaces.VendorID != vendorID || interfaces.ProductID != productID {
				return ATPort{}, USBInterfaces{}, fmt.Errorf(
					"QDC507 re-enumerated as unexpected USB identity %04x:%04x",
					interfaces.VendorID,
					interfaces.ProductID,
				)
			}
			port, portErr := a.portAtInterface(physicalDevice, previous.Interface)
			if portErr == nil && interfaces.voiceReady() {
				return port, interfaces, nil
			}
			lastErr = portErr
		} else {
			lastErr = inspectErr
		}
		select {
		case <-bounded.Done():
			if lastErr == nil {
				lastErr = bounded.Err()
			}
			return ATPort{}, USBInterfaces{}, fmt.Errorf("QDC507 did not return with complete ADB/UAC interfaces: %w", lastErr)
		case <-time.After(poll):
		}
	}
}

func (a *sysfsDeviceAccess) portAtInterface(physicalDevice, interfaceName string) (ATPort, error) {
	interfacePath := filepath.Join(physicalDevice, interfaceName)
	entries, err := os.ReadDir(interfacePath)
	if err != nil {
		return ATPort{}, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ttyUSB") || strings.HasPrefix(entry.Name(), "ttyACM") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return ATPort{}, fmt.Errorf("AT interface %s has no tty device", interfaceName)
	}
	sort.Strings(names)
	device := filepath.Join(a.devRoot, names[0])
	if _, err := os.Stat(device); err != nil {
		return ATPort{}, err
	}
	return ATPort{
		Device:          device,
		Interface:       interfaceName,
		SysfsDevicePath: filepath.Join(interfacePath, names[0]),
	}, nil
}

func cleanPhysicalDevice(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("QDC507 physical USB path is unavailable")
	}
	cleaned := filepath.Clean(value)
	if !filepath.IsAbs(cleaned) || !strings.HasPrefix(cleaned, defaultSysfsRoot+"/devices/") {
		return "", fmt.Errorf("QDC507 physical USB path is outside /sys/devices")
	}
	return cleaned, nil
}

func readHexAttribute(name string) (int, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 16, 32)
	if err != nil {
		return 0, err
	}
	return int(value), nil
}

var _ DeviceAccess = (*sysfsDeviceAccess)(nil)
