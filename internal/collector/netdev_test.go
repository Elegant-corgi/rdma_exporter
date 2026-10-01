package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCollectorPriorityBytes(t *testing.T) {
	t.Parallel()
	const metric = "rdma_netdev_prio_bytes_total"
	const header = "# HELP " + metric + " Bytes received or transmitted on the physical port by L2 priority (0-7). Ethtool {rx,tx}_prio[p]_bytes; includes all traffic on that priority, not only RDMA.\n# TYPE " + metric + " counter\n"
	for _, tc := range []struct {
		name                 string
		stats                map[string]uint64
		want                 string
		disabled, vf, shared bool
	}{
		{name: "priority5收发", stats: map[string]uint64{"rx_prio5_bytes": 67459853075894, "tx_prio5_bytes": 67461800120078}, want: header + metric + "{device=\"mlx5_0\",direction=\"rx\",netdev=\"ens1f0np0\",port=\"1\",priority=\"5\"} 6.7459853075894e+13\n" + metric + "{device=\"mlx5_0\",direction=\"tx\",netdev=\"ens1f0np0\",port=\"1\",priority=\"5\"} 6.7461800120078e+13\n"},
		{name: "边界优先级与真实零", stats: map[string]uint64{"rx_prio0_bytes": 0, "tx_prio7_bytes": 20, "rx_prio8_bytes": 9, "rx_prio05_bytes": 8, "rx_prio5_packets": 7, "rx_bytes_phy": 6, "rx_prio5_bytes_extra": 5}, want: header + metric + "{device=\"mlx5_0\",direction=\"rx\",netdev=\"ens1f0np0\",port=\"1\",priority=\"0\"} 0\n" + metric + "{device=\"mlx5_0\",direction=\"tx\",netdev=\"ens1f0np0\",port=\"1\",priority=\"7\"} 20\n"},
		{name: "缺失不补零", stats: map[string]uint64{"rx_prio5_packets": 7}},
		{name: "关闭ethtool", stats: map[string]uint64{"rx_prio5_bytes": 1}, disabled: true},
		{name: "VF沿用跳过规则", stats: map[string]uint64{"rx_prio5_bytes": 1}, vf: true},
		{name: "共享网卡只导出一次", stats: map[string]uint64{"rx_prio5_bytes": 1}, shared: true, want: header + metric + "{device=\"mlx5_0\",direction=\"rx\",netdev=\"ens1f0np0\",port=\"1\",priority=\"5\"} 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, stats := ethernetPFWithStats(tc.stats)
			provider.devices[0].IsVF = tc.vf
			if tc.shared {
				dev := provider.devices[0]
				dev.Name = "mlx5_1"
				provider.devices = append(provider.devices, dev)
			}
			var opts []Option
			if !tc.disabled {
				opts = append(opts, WithNetDevStatsProvider(stats))
			}
			c := New(provider, newDiscardLogger(), opts...)
			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(c)
			if err := testutil.GatherAndCompare(reg, strings.NewReader(tc.want), metric); err != nil {
				t.Fatal(err)
			}
			if tc.shared && stats.CallCount("ens1f0np0") != 1 {
				t.Fatal("共享网卡重复读取", stats.CallCount("ens1f0np0"))
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
