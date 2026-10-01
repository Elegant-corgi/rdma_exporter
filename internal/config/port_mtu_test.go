package config

import "testing"

func TestPortMTUFlags(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
		want, bad bool
	}{
		{"默认", "", nil, true, false}, {"环境关闭", "false", nil, false, false},
		{"正向覆盖", "false", []string{"--collector.port-mtu"}, true, false},
		{"反向关闭", "true", []string{"--no-collector.port-mtu"}, false, false},
		{"显式关闭", "", []string{"--collector.port-mtu=false"}, false, false},
		{"最后生效", "", []string{"--no-collector.port-mtu", "--collector.port-mtu"}, true, false},
		{"非法反向赋值", "", []string{"--no-collector.port-mtu=true"}, false, true},
		{"非法环境", "bad", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RDMA_EXPORTER_COLLECTOR_PORT_MTU", tc.env)
			cfg, err := Parse(tc.args)
			if (err != nil) != tc.bad || (!tc.bad && cfg.CollectorPortMTU != tc.want) {
				t.Fatalf("cfg=%+v err=%v", cfg, err)
			}
		})
	}
}
