package collector

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/yuuki/rdma_exporter/internal/rdma"
)

func TestPriorityTrafficMetricText(t *testing.T) {
	stub := &stubProvider{devices: []rdma.Device{{Name: "mlx5_0", Ports: []rdma.Port{{ID: 1, Attributes: rdma.PortAttributes{LinkLayer: "Ethernet", NetDev: "reth0"}}}}}}
	stats := newStubNetDevStatsProvider()
	stats.stats["reth0"] = map[string]uint64{"rx_prio0_bytes": 123, "tx_prio7_bytes": 456, "rx_prio7_packets": 0, "tx_prio0_packets": 9, "rx_prio8_bytes": 999, "rx0_bytes": 999, "rx_prio0_pause": 2}
	c := New(stub, slog.New(slog.NewTextHandler(io.Discard, nil)), WithNetDevStatsProvider(stats))
	expected := `# HELP rdma_netdev_prio_bytes_total Physical port bytes per L2 priority (0-7), including Ethernet and RoCE traffic. Ethtool rx_prio[p]_bytes or tx_prio[p]_bytes; not a per-queue or RDMA-only counter.
# TYPE rdma_netdev_prio_bytes_total counter
rdma_netdev_prio_bytes_total{device="mlx5_0",direction="rx",netdev="reth0",port="1",priority="0"} 123
rdma_netdev_prio_bytes_total{device="mlx5_0",direction="tx",netdev="reth0",port="1",priority="7"} 456
# HELP rdma_netdev_prio_packets_total Physical port packets per L2 priority (0-7), including Ethernet and RoCE traffic. Ethtool rx_prio[p]_packets or tx_prio[p]_packets; not a per-queue or RDMA-only counter.
# TYPE rdma_netdev_prio_packets_total counter
rdma_netdev_prio_packets_total{device="mlx5_0",direction="rx",netdev="reth0",port="1",priority="7"} 0
rdma_netdev_prio_packets_total{device="mlx5_0",direction="tx",netdev="reth0",port="1",priority="0"} 9
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "rdma_netdev_prio_bytes_total", "rdma_netdev_prio_packets_total"); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityTrafficGatesAndDedup(t *testing.T) {
	for _, tc := range []struct {
		name, layer                  string
		vf, enabled, shared, missing bool
		want                         int
	}{
		{"全部优先级", "Ethernet", false, true, false, false, 32},
		{"共享网卡", "Ethernet", false, true, true, false, 32},
		{"缺少字段", "Ethernet", false, true, false, true, 0},
		{"关闭采集", "Ethernet", false, false, false, false, 0},
		{"VF", "Ethernet", true, true, false, false, 0},
		{"非以太网", "InfiniBand", false, true, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev := rdma.Device{Name: "dev", IsVF: tc.vf, Ports: []rdma.Port{{ID: 1, Attributes: rdma.PortAttributes{LinkLayer: tc.layer, NetDev: "reth0"}}}}
			stub := &stubProvider{devices: []rdma.Device{dev}}
			if tc.shared {
				dev.Name = "dev2"
				stub.devices = append(stub.devices, dev)
			}
			stats := newStubNetDevStatsProvider()
			stats.stats["reth0"] = map[string]uint64{}
			if !tc.missing {
				for p := 0; p < 8; p++ {
					for _, d := range []string{"rx", "tx"} {
						for _, kind := range []string{"bytes", "packets"} {
							stats.stats["reth0"][fmt.Sprintf("%s_prio%d_%s", d, p, kind)] = uint64(p)
						}
					}
				}
			}
			var opts []Option
			if tc.enabled {
				opts = append(opts, WithNetDevStatsProvider(stats))
			}
			c := New(stub, slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
			if count := testutil.CollectAndCount(c, "rdma_netdev_prio_bytes_total", "rdma_netdev_prio_packets_total"); count != tc.want {
				t.Fatal(count, tc.want)
			}
			calls := stats.calls["reth0"]
			if tc.enabled && !tc.vf && tc.layer == "Ethernet" {
				if calls != 1 {
					t.Fatal("重复读取 ethtool", calls)
				}
			} else if calls != 0 {
				t.Fatal("不应读取 ethtool", calls)
			}
		})
	}
}
