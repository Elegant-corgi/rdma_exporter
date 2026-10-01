package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/yuuki/rdma_exporter/internal/rdma"
)

type fakeMTU struct {
	calls  []string
	values map[int]uint64
	err    error
}

func (p *fakeMTU) MTUs(_ context.Context, dev string) (map[int]uint64, error) {
	p.calls = append(p.calls, dev)
	return p.values, p.err
}

func TestMetadataMetricText(t *testing.T) {
	mtu := uint64(1500)
	stub := &stubProvider{devices: []rdma.Device{{Name: "mlx5_8", FirmwareVersion: "20.42.1000", Driver: "mlx5_core", DriverVersion: "23.10-2.1.3", NetDevs: []rdma.NetDev{{Name: "reth8", MTU: &mtu}}, Ports: []rdma.Port{{ID: 1, GIDs: []rdma.GID{{Index: 3, Address: "0000:0000:0000:0000:0000:ffff:6445:2519", Type: "RoCE v2", NetDev: "reth8", IPv4: "100.69.37.25"}}}}}}}
	query := &fakeMTU{values: map[int]uint64{1: 1024, 9: 4096}}
	c := New(stub, slog.New(slog.NewTextHandler(io.Discard, nil)), WithPortMTUProvider(query))
	expected := `# HELP rdma_device_info RDMA HCA firmware metadata.
# TYPE rdma_device_info gauge
rdma_device_info{device="mlx5_8",firmware_version="20.42.1000"} 1
# HELP rdma_driver_info RDMA device driver module version metadata.
# TYPE rdma_driver_info gauge
rdma_driver_info{driver="mlx5_core",version="23.10-2.1.3"} 1
# HELP rdma_gid_info Valid RDMA GID table entries; ipv4 is populated only for IPv4-mapped GIDs.
# TYPE rdma_gid_info gauge
rdma_gid_info{device="mlx5_8",gid="0000:0000:0000:0000:0000:ffff:6445:2519",gid_index="3",gid_type="RoCE v2",ipv4="100.69.37.25",netdev="reth8",port="1"} 1
# HELP rdma_netdev_mtu_bytes MTU of a network interface associated with an RDMA port, in bytes.
# TYPE rdma_netdev_mtu_bytes gauge
rdma_netdev_mtu_bytes{netdev="reth8"} 1500
# HELP rdma_port_active_mtu_bytes Current RDMA port active MTU in bytes; not the netdev MTU or a QP path MTU.
# TYPE rdma_port_active_mtu_bytes gauge
rdma_port_active_mtu_bytes{device="mlx5_8",port="1"} 1024
`
	names := []string{"rdma_device_info", "rdma_driver_info", "rdma_gid_info", "rdma_netdev_mtu_bytes", "rdma_port_active_mtu_bytes"}
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), names...); err != nil {
		t.Fatal(err)
	}
	stub.devices[0].Ports[0].GIDs = nil
	if count := testutil.CollectAndCount(c, "rdma_gid_info"); count != 0 {
		t.Fatal("删除的 GID 仍被导出", count)
	}
	stub.devices = append(stub.devices, stub.devices[0])
	stub.devices[1].Name = "mlx5_9"
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(c)
	if _, err := registry.Gather(); err != nil {
		t.Fatal("共享网卡/驱动重复导出", err)
	}
	if count := testutil.CollectAndCount(c, "rdma_driver_info", "rdma_netdev_mtu_bytes"); count != 2 {
		t.Fatal(count)
	}
}

func TestMTUFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      *fakeMTU
		wantErrors float64
	}{
		{"工具不可用", nil, 0}, {"执行失败", &fakeMTU{err: errors.New("exit 1")}, 1},
		{"缺端口", &fakeMTU{values: map[int]uint64{2: 4096}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubProvider{devices: []rdma.Device{{Name: "dev", FirmwareVersion: "fw", Ports: []rdma.Port{{ID: 1}}}}}
			var provider PortMTUProvider
			if tc.query != nil {
				provider = tc.query
			}
			c := New(stub, slog.New(slog.NewTextHandler(io.Discard, nil)), WithPortMTUProvider(provider))
			if count := testutil.CollectAndCount(c, "rdma_device_info", "rdma_port_active_mtu_bytes"); count != 1 {
				t.Fatal("查询失败影响固件或导出了部分 MTU", count)
			}
			if got := testutil.ToFloat64(c.metadata.errors); got != tc.wantErrors {
				t.Fatal(got)
			}
		})
	}
}

func TestH100MetricCountsAndExclusion(t *testing.T) {
	sysfs := rdma.NewSysfsProvider()
	sysfs.SetSysfsRoot(filepath.Join("..", "rdma", "testdata", "sysfs", "h100"))
	devices, err := sysfs.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 静态 fixture 的驱动符号链接另由 Linux provider 测试验证。
	for i := range devices {
		devices[i].Driver = "mlx5_core"
		devices[i].DriverVersion = "23.10-2.1.3"
	}
	query := &fakeMTU{values: map[int]uint64{1: 4096}}
	c := New(&stubProvider{devices: devices}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithPortMTUProvider(query))
	count := testutil.CollectAndCount(c, "rdma_device_info", "rdma_driver_info", "rdma_gid_info", "rdma_netdev_mtu_bytes", "rdma_port_active_mtu_bytes")
	if count != 64 || len(query.calls) != 9 {
		t.Fatal(count, query.calls)
	}
	sysfs.SetExcludeDevices([]string{"mlx5_8"})
	query.calls = nil
	c = New(sysfs, slog.New(slog.NewTextHandler(io.Discard, nil)), WithPortMTUProvider(query))
	testutil.CollectAndCount(c, "rdma_port_active_mtu_bytes")
	if len(query.calls) != 8 {
		t.Fatal(query.calls)
	}
	for _, name := range query.calls {
		if name == "mlx5_8" {
			t.Fatal("查询了被排除设备")
		}
	}
	query.calls = nil
	c = New(sysfs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if count := testutil.CollectAndCount(c, "rdma_port_active_mtu_bytes", "rdma_port_mtu_scrape_errors_total"); count != 0 {
		t.Fatal("关闭后仍导出", count)
	}
}
