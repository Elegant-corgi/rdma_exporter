package rdma

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GID 保留每个有效表项，IPv4 仅来自 IPv4-mapped 地址。
type GID struct {
	Index                       int
	Address, Type, NetDev, IPv4 string
}

// NetDev 的 MTU 为 nil 时表示未成功读取，不能当作零导出。
type NetDev struct {
	Name string
	MTU  *uint64
}

func (p *SysfsProvider) SetLogger(logger *slog.Logger) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = logger
}

func (p *SysfsProvider) metadataWarning(device string, port int, path string, err error) {
	p.mu.RLock()
	logger := p.logger
	p.mu.RUnlock()
	if logger != nil {
		logger.Warn("读取 RDMA 元数据失败", "device", device, "port", port, "path", path, "err", err)
	}
}

func (p *SysfsProvider) metadataRead(device string, port int, path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			p.metadataWarning(device, port, path, err)
		}
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (p *SysfsProvider) readMetadata(ctx context.Context, root string, devices []Device) error {
	netCache := make(map[string]NetDev)
	moduleCache := make(map[string]string)
	for i := range devices {
		if err := ctx.Err(); err != nil {
			return err
		}
		dev := &devices[i]
		base := filepath.Join(root, classInfinibandPath, dev.Name)
		dev.FirmwareVersion = p.metadataRead(dev.Name, 0, filepath.Join(base, "fw_ver"))
		driverPath := filepath.Join(base, "device", "driver")
		if link, err := os.Readlink(driverPath); err == nil {
			dev.Driver = filepath.Base(link)
			if module, err := os.Readlink(filepath.Join(driverPath, "module")); err == nil {
				name := filepath.Base(module)
				version, ok := moduleCache[name]
				if !ok {
					version = p.metadataRead(dev.Name, 0, filepath.Join(root, "module", name, "version"))
					moduleCache[name] = version
				}
				dev.DriverVersion = version
			} else if !errors.Is(err, os.ErrNotExist) {
				p.metadataWarning(dev.Name, 0, filepath.Join(driverPath, "module"), err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			p.metadataWarning(dev.Name, 0, driverPath, err)
		}
		names := make(map[string]int)
		for j := range dev.Ports {
			port := &dev.Ports[j]
			dir := filepath.Join(base, "ports", strconv.Itoa(port.ID))
			gids, err := p.readGIDs(ctx, dev.Name, port.ID, dir)
			if err != nil {
				return err
			}
			port.GIDs = gids
			if port.Attributes.NetDev != "" {
				names[port.Attributes.NetDev] = port.ID
			}
			for _, gid := range gids {
				if gid.NetDev != "" {
					names[gid.NetDev] = port.ID
				}
			}
		}
		sorted := make([]string, 0, len(names))
		for name := range names {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			if err := ctx.Err(); err != nil {
				return err
			}
			if name == "." || name == ".." || strings.ContainsAny(name, `/\:`) {
				continue
			}
			net, ok := netCache[name]
			if !ok {
				net.Name = name
				path := filepath.Join(root, "class", "net", name, "mtu")
				raw := p.metadataRead(dev.Name, names[name], path)
				if raw != "" {
					mtu, err := strconv.ParseUint(raw, 10, 64)
					if err == nil && mtu > 0 {
						net.MTU = &mtu
					} else {
						p.metadataWarning(dev.Name, names[name], path, fmt.Errorf("无效 MTU %q", raw))
					}
				}
				netCache[name] = net
			}
			dev.NetDevs = append(dev.NetDevs, net)
		}
	}
	return nil
}

func (p *SysfsProvider) readGIDs(ctx context.Context, device string, port int, dir string) ([]GID, error) {
	path := filepath.Join(dir, "gids")
	entries, err := os.ReadDir(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			p.metadataWarning(device, port, path, err)
		}
		return nil, nil
	}
	type indexed struct {
		index int
		name  string
	}
	var indices []indexed
	for _, entry := range entries {
		n, err := strconv.Atoi(entry.Name())
		if err == nil && n >= 0 && !entry.IsDir() {
			indices = append(indices, indexed{n, entry.Name()})
		}
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i].index < indices[j].index })
	var gids []GID
	for _, entry := range indices {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file := filepath.Join(path, entry.name)
		raw := p.metadataRead(device, port, file)
		if raw == "" {
			continue
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			p.metadataWarning(device, port, file, fmt.Errorf("无效 GID %q", raw))
			continue
		}
		if addr.IsUnspecified() {
			continue
		}
		b := addr.As16()
		groups := make([]string, 8)
		for i := range groups {
			groups[i] = fmt.Sprintf("%04x", binary.BigEndian.Uint16(b[2*i:2*i+2]))
		}
		gid := GID{Index: entry.index, Address: strings.Join(groups, ":"), Type: p.metadataRead(device, port, filepath.Join(dir, "gid_attrs", "types", entry.name)), NetDev: p.metadataRead(device, port, filepath.Join(dir, "gid_attrs", "ndevs", entry.name))}
		if addr.Is4In6() {
			gid.IPv4 = addr.Unmap().String()
		}
		gids = append(gids, gid)
	}
	return gids, nil
}
