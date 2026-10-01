package collector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCollectorPhysicalPortBytes(t *testing.T) {
	t.Parallel()
	const metric = "rdma_netdev_phy_bytes_total"
	const help = "Physical port bytes received or transmitted, including Ethernet and RDMA traffic. Ethtool rx_bytes_phy or tx_bytes_phy; not RDMA-only or per-priority."
	type sample struct {
		direction string
		bytes     uint64
	}
	for _, tc := range []struct {
		name                                      string
		stats                                     map[string]uint64
		want                                      []sample
		disabled, vf, ib, shared, noSRIOV, failed bool
	}{
		{name: "物理端口收发", stats: map[string]uint64{"rx_bytes_phy": 186079036, "tx_bytes_phy": 186078904}, want: []sample{{"rx", 186079036}, {"tx", 186078904}}},
		{name: "单方向缺失不补零", stats: map[string]uint64{"tx_bytes_phy": 42}, want: []sample{{"tx", 42}}},
		{name: "真实零值保留", stats: map[string]uint64{"rx_bytes_phy": 0}, want: []sample{{"rx", 0}}},
		{name: "其他口径不误匹配", stats: map[string]uint64{"rx_bytes": 1, "rx_prio5_bytes": 2, "rx_vport_rdma_unicast_bytes": 3, "rx_bits_phy": 4, "rx_packets_phy": 5, "rx_bytes_phy_extra": 6}},
		{name: "关闭ethtool", stats: map[string]uint64{"rx_bytes_phy": 1}, disabled: true},
		{name: "VF沿用跳过规则", stats: map[string]uint64{"rx_bytes_phy": 1}, vf: true},
		{name: "IB链路不采集", stats: map[string]uint64{"rx_bytes_phy": 1}, ib: true},
		{name: "ethtool失败不导出", stats: map[string]uint64{"rx_bytes_phy": 1}, failed: true},
		{name: "共享网卡只读取导出一次", stats: map[string]uint64{"rx_bytes_phy": 10, "tx_bytes_phy": 20}, shared: true, want: []sample{{"rx", 10}, {"tx", 20}}},
		{name: "不要求SRIOV能力", stats: map[string]uint64{"rx_bytes_phy": 10}, noSRIOV: true, want: []sample{{"rx", 10}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, stats := ethernetPFWithStats(tc.stats)
			provider.devices[0].IsVF = tc.vf
			if tc.noSRIOV {
				provider.devices[0].HasSRIOV = false
			}
			if tc.ib {
				provider.devices[0].Ports[0].Attributes.LinkLayer = "InfiniBand"
			}
			if tc.shared {
				dev := provider.devices[0]
				dev.Name = "mlx5_1"
				provider.devices = append(provider.devices, dev)
			}
			if tc.failed {
				stats.errs["ens1f0np0"] = errors.New("read failure")
			}
			var opts []Option
			if !tc.disabled {
				opts = append(opts, WithNetDevStatsProvider(stats))
			}
			c := New(provider, newDiscardLogger(), opts...)
			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(c)
			var expected strings.Builder
			if len(tc.want) > 0 {
				fmt.Fprintf(&expected, "# HELP %s %s\n# TYPE %s counter\n", metric, help, metric)
				for _, s := range tc.want {
					fmt.Fprintf(&expected, "%s{device=\"mlx5_0\",direction=\"%s\",netdev=\"ens1f0np0\",port=\"1\"} %d\n", metric, s.direction, s.bytes)
				}
			}
			if err := testutil.GatherAndCompare(reg, strings.NewReader(expected.String()), metric); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if tc.disabled || tc.vf || tc.ib {
				wantCalls = 0
			}
			if got := stats.CallCount("ens1f0np0"); got != wantCalls {
				t.Fatalf("ethtool 读取次数=%d，期望=%d", got, wantCalls)
			}
		})
	}
}

func TestIsRepresentorPhysPortName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want bool
	}{
		{name: "p0", want: false},
		{name: "", want: false},
		{name: "1", want: false},
		{name: "pf0vf1", want: true},
		{name: "pf0sf1", want: true},
		{name: "c1pf0vf0", want: true},
		{name: "c1pf0sf0", want: true},
		{name: "pf0hpf", want: true},
		{name: "c1pf0hpf", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRepresentorPhysPortName(tc.name); got != tc.want {
				t.Fatalf("isRepresentorPhysPortName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestReadNetDevPhysPortNameFromTrimsValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	netDir := filepath.Join(root, "class", "net", "ens1f0np0")
	if err := os.MkdirAll(netDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "phys_port_name"), []byte("pf0vf1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readNetDevPhysPortNameFrom(root, "ens1f0np0")
	if got != "pf0vf1" {
		t.Fatalf("got %q, want pf0vf1", got)
	}
}
