package rdma

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeMetadataFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestH100Metadata(t *testing.T) {
	p := NewSysfsProvider()
	p.SetSysfsRoot(filepath.Join("testdata", "sysfs", "h100"))
	devices, err := p.Devices(context.Background())
	if err != nil || len(devices) != 9 {
		t.Fatalf("devices=%v err=%v", devices, err)
	}
	for i, dev := range devices {
		fw, mtu := "28.41.1000", uint64(4200)
		if i == 8 {
			fw = "20.42.1000"
			mtu = 1500
		}
		if dev.FirmwareVersion != fw || len(dev.NetDevs) != 1 || dev.NetDevs[0].MTU == nil || *dev.NetDevs[0].MTU != mtu {
			t.Fatalf("device=%+v", dev)
		}
		gids := dev.Ports[0].GIDs
		if len(gids) != 4 || gids[0].IPv4 != "" || gids[2].IPv4 == "" || gids[0].Address != gids[1].Address || gids[0].Type == gids[1].Type {
			t.Fatal(gids)
		}
	}
	p.SetExcludeDevices([]string{"mlx5_8"})
	devices, err = p.Devices(context.Background())
	if err != nil || len(devices) != 8 {
		t.Fatal(len(devices), err)
	}
}

func TestMetadataEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name, mtu string
		wantMTU   bool
	}{{"有效", "1500", true}, {"零", "0", false}, {"负数", "-1", false}, {"无效", "abc", false}, {"缺失", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			port := filepath.Join(root, "class", "infiniband", "dev", "ports", "1")
			writeMetadataFile(t, filepath.Join(port, "gids", "0"), "::")
			writeMetadataFile(t, filepath.Join(port, "gids", "2"), "::ffff:100.69.37.25")
			writeMetadataFile(t, filepath.Join(port, "gids", "10"), "2001:DB8::1")
			writeMetadataFile(t, filepath.Join(port, "gids", "11"), "bad")
			writeMetadataFile(t, filepath.Join(port, "gid_attrs", "ndevs", "2"), "reth8")
			writeMetadataFile(t, filepath.Join(port, "gid_attrs", "ndevs", "10"), "vlan8")
			if tc.mtu != "" {
				writeMetadataFile(t, filepath.Join(root, "class", "net", "reth8", "mtu"), tc.mtu)
			}
			writeMetadataFile(t, filepath.Join(root, "class", "net", "vlan8", "mtu"), "9000")
			p := NewSysfsProvider()
			p.SetSysfsRoot(root)
			devices, err := p.Devices(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			dev := devices[0]
			gids := dev.Ports[0].GIDs
			if len(gids) != 2 || gids[0].Index != 2 || gids[1].Index != 10 || gids[0].IPv4 != "100.69.37.25" || gids[0].Type != "" || gids[1].Address != "2001:0db8:0000:0000:0000:0000:0000:0001" || gids[1].IPv4 != "" {
				t.Fatal(gids)
			}
			if len(dev.NetDevs) != 2 || (dev.NetDevs[0].MTU != nil) != tc.wantMTU {
				t.Fatal(dev.NetDevs)
			}
			// 第一个非空 ndev 保持字典序选择，不能因 GID 数字排序改变现有端口标签。
			if dev.Ports[0].Attributes.NetDev != "vlan8" {
				t.Fatal(dev.Ports[0].Attributes.NetDev)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := p.Devices(ctx); err == nil {
				t.Fatal("应响应取消")
			}
		})
	}
}

func TestDriverMetadata(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("需要 Linux sysfs 符号链接语义")
	}
	root := t.TempDir()
	base := filepath.Join(root, "class", "infiniband", "dev")
	writeMetadataFile(t, filepath.Join(base, "fw_ver"), " 20.42.1000 \n")
	pci := filepath.Join(root, "bus", "pci", "devices", "0000:01:00.0")
	driver := filepath.Join(root, "bus", "pci", "drivers", "mlx5_core")
	for _, dir := range []string{pci, driver, filepath.Join(root, "module", "mlx5_core")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range [][2]string{{pci, filepath.Join(base, "device")}, {driver, filepath.Join(pci, "driver")}, {filepath.Join(root, "module", "mlx5_core"), filepath.Join(driver, "module")}} {
		if err := os.Symlink(link[0], link[1]); err != nil {
			t.Fatal(err)
		}
	}
	writeMetadataFile(t, filepath.Join(root, "module", "mlx5_core", "version"), " 23.10-2.1.3 \n")
	p := NewSysfsProvider()
	p.SetSysfsRoot(root)
	devices, err := p.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dev := devices[0]; dev.Driver != "mlx5_core" || dev.DriverVersion != "23.10-2.1.3" || dev.FirmwareVersion != "20.42.1000" {
		t.Fatal(dev)
	}
}

func TestMetadataNetDevNames(t *testing.T) {
	for _, name := range []string{"ib..0", ".", "..", "../outside", `..\outside`} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeMetadataFile(t, filepath.Join(root, "class", "net", "ib..0", "mtu"), "4096")
			devices := []Device{{Name: "dev", Ports: []Port{{ID: 1, Attributes: PortAttributes{NetDev: name}}}}}
			if err := NewSysfsProvider().readMetadata(context.Background(), root, devices); err != nil {
				t.Fatal(err)
			}
			if name == "ib..0" {
				if len(devices[0].NetDevs) != 1 || devices[0].NetDevs[0].MTU == nil || *devices[0].NetDevs[0].MTU != 4096 {
					t.Fatal(devices)
				}
			} else if len(devices[0].NetDevs) != 0 {
				t.Fatal("不应读取路径逃逸网卡名", devices)
			}
		})
	}
}
