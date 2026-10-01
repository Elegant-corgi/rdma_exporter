package collector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/yuuki/rdma_exporter/internal/rdma"
)

type PortMTUProvider interface {
	MTUs(context.Context, string) (map[int]uint64, error)
}
type metadataMetrics struct {
	firmware, driver, gid, netMTU, portMTU *prometheus.Desc
	errors                                 prometheus.Counter
	enabled                                bool
	provider                               PortMTUProvider
}

func WithPortMTUProvider(provider PortMTUProvider) Option {
	return func(c *RdmaCollector) { c.metadata.enabled = true; c.metadata.provider = provider }
}

func (c *RdmaCollector) initMetadataMetrics() {
	m := &c.metadata
	m.firmware = prometheus.NewDesc("rdma_device_info", "RDMA HCA firmware metadata.", []string{"device", "firmware_version"}, nil)
	m.driver = prometheus.NewDesc("rdma_driver_info", "RDMA device driver module version metadata.", []string{"driver", "version"}, nil)
	m.gid = prometheus.NewDesc("rdma_gid_info", "Valid RDMA GID table entries; ipv4 is populated only for IPv4-mapped GIDs.", []string{"device", "port", "gid_index", "gid", "gid_type", "netdev", "ipv4"}, nil)
	m.netMTU = prometheus.NewDesc("rdma_netdev_mtu_bytes", "MTU of a network interface associated with an RDMA port, in bytes.", []string{"netdev"}, nil)
	m.portMTU = prometheus.NewDesc("rdma_port_active_mtu_bytes", "Current RDMA port active MTU in bytes; not the netdev MTU or a QP path MTU.", []string{"device", "port"}, nil)
	m.errors = prometheus.NewCounter(prometheus.CounterOpts{Name: "rdma_port_mtu_scrape_errors_total", Help: "Total device queries that failed while collecting RDMA port active MTU."})
}

func (c *RdmaCollector) describeMetadata(ch chan<- *prometheus.Desc) {
	m := &c.metadata
	for _, desc := range []*prometheus.Desc{m.firmware, m.driver, m.gid, m.netMTU} {
		ch <- desc
	}
	if m.enabled {
		ch <- m.portMTU
		m.errors.Describe(ch)
	}
}

func (c *RdmaCollector) collectMetadata(ctx context.Context, ch chan<- prometheus.Metric, devices []rdma.Device) {
	m := &c.metadata
	drivers := make(map[[2]string]bool)
	nets := make(map[string]bool)
	emit := func(desc *prometheus.Desc, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
	// 先导出 sysfs 数据，命令超时不阻止这些数据进入本次采集。
	for _, dev := range devices {
		if dev.FirmwareVersion != "" {
			emit(m.firmware, 1, dev.Name, dev.FirmwareVersion)
		}
		key := [2]string{dev.Driver, dev.DriverVersion}
		if key[0] != "" && key[1] != "" && !drivers[key] {
			drivers[key] = true
			emit(m.driver, 1, key[0], key[1])
		}
		for _, net := range dev.NetDevs {
			if net.MTU != nil && *net.MTU > 0 && !nets[net.Name] {
				nets[net.Name] = true
				emit(m.netMTU, float64(*net.MTU), net.Name)
			}
		}
		for _, port := range dev.Ports {
			for _, gid := range port.GIDs {
				emit(m.gid, 1, dev.Name, strconv.Itoa(port.ID), strconv.Itoa(gid.Index), gid.Address, gid.Type, gid.NetDev, gid.IPv4)
			}
		}
	}
	if !m.enabled || m.provider == nil {
		return
	}
	for _, dev := range devices {
		if ctx.Err() != nil {
			break
		}
		if len(dev.Ports) == 0 {
			continue
		}
		start := time.Now()
		values, err := m.provider.MTUs(ctx, dev.Name)
		if err == nil {
			for _, port := range dev.Ports {
				if values[port.ID] == 0 {
					err = fmt.Errorf("端口 %d 缺少 active_mtu", port.ID)
					break
				}
			}
		}
		if err != nil {
			m.errors.Inc()
			c.logger.Warn("查询 RDMA 端口 MTU 失败", "device", dev.Name, "duration", time.Since(start), "err", err)
			continue
		}
		for _, port := range dev.Ports {
			emit(m.portMTU, float64(values[port.ID]), dev.Name, strconv.Itoa(port.ID))
		}
	}
}
