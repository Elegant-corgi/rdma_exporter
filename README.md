# Prometheus RDMA Exporter

## 按二层优先级统计的端口流量

复用默认开启的 `--collector.ethtool`，映射 `rx_prio[0-7]_{bytes,packets}`、`tx_prio[0-7]_{bytes,packets}`：

| 指标 | 标签 | 含义 |
| --- | --- | --- |
| `rdma_netdev_prio_bytes_total` | `device,port,netdev,direction,priority` | 按方向及二层优先级统计的累计字节数 |
| `rdma_netdev_prio_packets_total` | 同上 | 按方向及二层优先级统计的累计包数 |

`direction` 为 `rx` / `tx`，`priority` 为 `0`–`7`。这些是物理端口按二层优先级的流量，
不是硬件队列编号，也不是 RDMA 专属或 PFC pause 计数器；包括 Ethernet/RoCE 流量。
口径见 [Linux mlx5 文档](https://docs.kernel.org/networking/device_drivers/ethernet/mellanox/mlx5/counters.html#priority-port-counters)。
沿用现有 Ethernet、非已识别 PCI VF 的采集条件，每个 netdev 只读取、导出一次。
只导出驱动实际返回的字段，缺失不补零，实际返回零则正常导出；全部字段存在时每个 netdev 新增 32 条序列。
关闭 ethtool 时这两组指标同时关闭，无新增命令或依赖。

示例值仅说明格式：

```prometheus
rdma_netdev_prio_bytes_total{device="mlx5_0",port="1",netdev="reth0",direction="rx",priority="3"} 123456
rdma_netdev_prio_bytes_total{device="mlx5_0",port="1",netdev="reth0",direction="tx",priority="3"} 234567
rdma_netdev_prio_packets_total{device="mlx5_0",port="1",netdev="reth0",direction="rx",priority="3"} 1000
rdma_netdev_prio_packets_total{device="mlx5_0",port="1",netdev="reth0",direction="tx",priority="3"} 2000
```

字节速率使用 `rate(rdma_netdev_prio_bytes_total[5m])`（B/s），乘以 8 得到 bit/s；包速率使用 `rate(rdma_netdev_prio_packets_total[5m])`。
不要与 sysfs、QP 或 vport RDMA 计数器相加，也不要跨不同 netdev 汇总后假定对应不同物理端口。

## RDMA 运维元数据

默认新增以下只读指标，独立于 ethtool 开关：

| 指标 | 标签 | 含义 |
| --- | --- | --- |
| `rdma_netdev_mtu_bytes` | `netdev` | 关联网络接口的 MTU，单位字节，同一网卡只导出一次 |
| `rdma_port_active_mtu_bytes` | `device,port` | 当前 RDMA 端口 active_mtu，单位字节，不等同于网卡 MTU或具体 QP 的路径 MTU |
| `rdma_device_info` | `device,firmware_version` | HCA 固件版本，值为 1 |
| `rdma_driver_info` | `driver,version` | 实际关联驱动的模块版本，值为 1，按驱动和版本去重 |
| `rdma_gid_info` | `device,port,gid_index,gid,gid_type,netdev,ipv4` | 每个有效 GID 表项，值为 1 |
| `rdma_port_mtu_scrape_errors_total` | 无 | 执行或解析失败的设备 MTU 查询累计次数 |

GID 使用小写完整 IPv6 格式，保留 sysfs 类型原文；相同 GID 的 v1/v2 和不同 index 分别导出。
`ipv4` 仅从 IPv4-mapped GID 派生，其他 GID 的该标签为空。全零槽位过滤；有效 GID 的类型或网卡属性缺失时对应标签为空。
网卡 MTU、固件、驱动和 GID 来自 `--sysfs-root` 指定的 sysfs；缺失或无效字段省略，不填零、不使用旧值。
现有 `rdma_port_info` 的 netdev 选择规则和标签保持不变。地址变化会产生新的 GID 时间序列，旧表项不再导出。

端口 MTU 默认通过 `ibv_devinfo -d <device>` 查询，需要工具在 PATH 中。
可使用 `--no-collector.port-mtu` 或 `--collector.port-mtu=false` 关闭；环境变量为
`RDMA_EXPORTER_COLLECTOR_PORT_MTU`（默认 `true`），CLI 优先、最后显式开关生效。
工具缺失时启动警告一次并停用该查询，其余指标继续采集；安装工具后需要重启 exporter。
查询按设备顺序执行，每设备最多 1 秒并遵守剩余 scrape 时间，输出最多 64 KiB；失败设备的 MTU 不导出。
被排除的设备不执行查询。该命令访问当前运行环境的 RDMA 设备，`--sysfs-root` 不重定向它；只导出 sysfs 快照中存在的端口。

保持无 CGO 静态构建。现有发布镜像没有 `ibv_devinfo`，容器若需要 active_mtu，须自行提供该工具、
libibverbs/provider 等运行依赖和匹配的 RDMA 设备访问权限（通常包括 `/dev/infiniband`），同时保证 sysfs 可读。
不需要 exporter 修改统计状态、创建或绑定 QP。本功能不自动调整镜像、systemd 或设备权限。

现场样例 `/metrics`（展示 mlx5_8 的部分表项）：

```prometheus
rdma_netdev_mtu_bytes{netdev="reth8"} 1500
rdma_port_active_mtu_bytes{device="mlx5_8",port="1"} 1024
rdma_device_info{device="mlx5_8",firmware_version="20.42.1000"} 1
rdma_driver_info{driver="mlx5_core",version="23.10-2.1.3"} 1
rdma_gid_info{device="mlx5_8",port="1",gid_index="0",gid="fe80:0000:0000:0000:5aa2:e1ff:fe3d:2346",gid_type="IB/RoCE v1",netdev="reth8",ipv4=""} 1
rdma_gid_info{device="mlx5_8",port="1",gid_index="3",gid="0000:0000:0000:0000:0000:ffff:6445:2519",gid_type="RoCE v2",netdev="reth8",ipv4="100.69.37.25"} 1
rdma_port_mtu_scrape_errors_total 0
```

本次现场九设备样例预期新增 64 条业务时间序列：9 网卡 MTU + 9 端口 MTU + 9 固件 + 1 驱动 + 36 GID，不含错误计数器。

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI](https://github.com/yuuki/rdma_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/yuuki/rdma_exporter/actions/workflows/ci.yml)
[![GitHub release](https://img.shields.io/github/v/release/yuuki/rdma_exporter)](https://github.com/yuuki/rdma_exporter/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/yuuki/rdma_exporter.svg)](https://pkg.go.dev/github.com/yuuki/rdma_exporter)

For details on GitHub Actions status badges, see the [official documentation](https://docs.github.com/ja/actions/how-tos/monitor-workflows/add-a-status-badge).

`rdma_exporter` collects RDMA (InfiniBand/RoCE) NIC statistics from Linux hosts and exposes them as Prometheus metrics. The exporter walks the kernel's sysfs tree directly and publishes metrics with [`github.com/prometheus/client_golang`](https://pkg.go.dev/github.com/prometheus/client_golang).

## Overview

![rdma_exporter architecture overview](docs/images/architecture-overview.png)

## Features

- Publishes counters from `/sys/class/infiniband/<dev>/<port>/counters` and `/hw_counters` as `rdma_<counter>_total` metrics that match NVIDIA's *Understanding mlx5 Linux Counters and Status Parameters* guide (e.g. `rdma_port_rcv_data_total`, `rdma_symbol_error_total`, `rdma_duplicate_request_total`). The sysfs `lifespan` knob is exported as the gauge `rdma_lifespan_milliseconds`, not as a `_total` counter.
- Exposes port metadata (link layer, state, width, speed, netdev, PCI address, VF/PF relationship, etc.) through `rdma_port_info`.
- Tracks scrape failures with `rdma_scrape_errors_total`.
- **Supports device exclusion** (`--exclude-devices`) to prevent kernel log flooding on firmware-restricted devices (NVIDIA DGX, Umbriel, GB200 systems).
- Ships with an HTTP server that serves `/metrics` and `/healthz` and gracefully shuts down on `SIGINT`/`SIGTERM`.
- Supports an alternative sysfs root (`--sysfs-root`) for testing or chroot environments.
- Honors a configurable scrape timeout (`--scrape-timeout`) to protect long-running sysfs reads.
- Exports RoCEv2 PFC and mlx5 ethtool hardware counters (NIC buffer drops, PCIe stalls, PHY/FEC, IEEE 802.3x global pause, pause storm, vport RDMA) via `--collector.ethtool` (default on; disable with `--no-collector.ethtool`).
- Exports mlx5 congestion-control counters (`cc_*`) and link-wide optional traffic (`rdma_optional_{rx,tx}_{bytes,packets}_total`) that sysfs omits, via RDMA netlink (`--collector.optional-counters`, default on). The exporter never enables counters.
- Optionally exports live auto-type QP hardware counters (`rdma_qp_*`) via a separate RDMA netlink socket (`--collector.qp-counters`, default off). The dump can exhaust the 5s scrape timeout on dense hosts; the exporter never binds QPs or enables auto mode.

## Requirements

- Go 1.27.0 or newer.
- Linux with read access to `/sys/class/infiniband` (for production use).

## Build

Windows Git Bash 编译 Linux 二进制，请看 [make 编译与试跑手册](docs/build.md)。设置目标平台后直接使用 `make build`；每次都会调用 Go 构建，由 Go 缓存处理增量编译。

```bash
go build -o rdma_exporter .
```

Alternatively, use the provided `Makefile` helpers:

```bash
make build   # compiles ./rdma_exporter
make test    # runs go test ./...
make lint    # runs go vet ./...
```

## Run

```bash
./rdma_exporter \
  --listen-address=":9879" \
  --metrics-path="/metrics" \
  --health-path="/healthz"
```

To exclude specific devices that trigger firmware errors (e.g., on NVIDIA DGX/GB200 systems):

```bash
./rdma_exporter --exclude-devices=mlx5_0,mlx5_1
```

Optional mlx5 congestion-control counters (`cc_*`) are omitted from sysfs. They are **disabled by default** and `rdma statistic set` is **not persistent**: enablement lives in the in-kernel `rdma_hw_stats` bitmap (and, on mlx5, in flow-steering rules created at enable time). A reboot, `mlx5_ib` reload, or device unbind/rebind returns them to disabled. There is no sysfs, module parameter, or `mlxconfig` knob. Reading them needs Linux 5.16+ (`STAT_GET_STATUS`). The exporter never calls `rdma statistic set`.

`rdma statistic set link DEV/PORT optional-counters A,B` **replaces** the enabled set. Counters not listed are disabled, including other mlx5 optional counters such as `rdma_rx_packets` / `rdma_tx_bytes`. Enabling optional counters allocates mlx5 flow counters and steering rules and may affect datapath performance; measure before leaving them on fleet-wide.

Enable once (`CAP_NET_ADMIN`), then scrape. Confirm with `rdma statistic mode` before trusting `/metrics`:

```bash
rdma statistic mode supported link mlx5_0/1
sudo rdma statistic set link mlx5_0/1 optional-counters cc_rx_ce_pkts,cc_rx_cnp_pkts,cc_tx_cnp_pkts
rdma statistic mode link mlx5_0/1
./rdma_exporter
```

To re-apply after boot or hotplug, use a root oneshot (not the `rdma_exporter` user). The repository ships [`deploy/scripts/rdma-enable-hardware-counters.sh`](deploy/scripts/rdma-enable-hardware-counters.sh), which **owns** the complete optional-counter set as the three `cc_*` names by default; override `RDMA_OPTIONAL_COUNTERS` to keep other mlx5 optional names in the same `set` line. Skip ports that do not advertise `cc_*`, and do not hide `set` failures.

Install the script, systemd unit, and udev rule like other RDMA user services ([rdma-core udev.md](https://github.com/linux-rdma/rdma-core/blob/master/Documentation/udev.md)). Do not use udev `RUN+=` (it blocks the udev queue):

```bash
sudo install -Dm0755 deploy/scripts/rdma-enable-hardware-counters.sh \
  /usr/local/sbin/rdma-enable-hardware-counters
sudo install -Dm0644 deploy/systemd/rdma-hardware-counters.service \
  /etc/systemd/system/rdma-hardware-counters.service
sudo install -Dm0644 deploy/udev/90-rdma-hardware-counters.rules \
  /etc/udev/rules.d/90-rdma-hardware-counters.rules
sudo systemctl daemon-reload
sudo systemctl enable rdma-hardware-counters.service
```

Optional: copy [`deploy/systemd/rdma-hardware-counters.env.example`](deploy/systemd/rdma-hardware-counters.env.example) to `/etc/rdma-hardware-counters.env` and uncomment `RDMA_ENABLE_QP_COUNTERS=1` when scraping `--collector.qp-counters`. The script then extends the optional-counter set with `rdma_{rx,tx}_{bytes,packets}` and runs `rdma statistic qp set link ... auto type on optional-counters on` on each supported port. Install [`deploy/systemd/rdma_exporter-qp-counters.conf.example`](deploy/systemd/rdma_exporter-qp-counters.conf.example) as `/etc/systemd/system/rdma_exporter.service.d/qp-counters.conf` so the exporter enables the QP collector.

`enable` without `--now` lets the first hardware appearance start `rdma-hw.target`. `SYSTEMD_WANTS` re-runs the oneshot on later `add` events (VF, driver reload). If `rdma-hw.target` is absent, use `After=network-online.target` and `WantedBy=multi-user.target`, and keep the udev rule for hotplug. After applying, `rdma statistic mode` should list the intended Optional-set.

Live QP statistic sets are a different path. The kernel's sysfs `hw_counters` already include the default pool plus every running allocated set plus history. A QP dump is only the sets currently bound; **do not add** `rdma_qp_*_total` to `rdma_<name>_total`. Auto mode applies to **new user QPs that go RST→INIT after enablement**; existing QPs and kernel QPs stay on the default pool. Enable auto type **before** the workload creates QPs (`CAP_NET_ADMIN`). `STAT_GET` / dump is unprivileged. The exporter never issues `STAT_SET`, bind, unbind, or `qp set auto`.

`auto pid` and manual per-QP sets are visible on mode/mask gauges only; their values are not exported. Optional traffic dump keys (`rdma_rx_bytes`, `rdma_tx_bytes`, `rdma_rx_packets`, `rdma_tx_packets`) are exported from the same auto-type dump as `rdma_qp_rx_bytes_total` (and the tx/packets analogues). They appear only when the dump contains those keys. The exporter never enables them.

```bash
sudo rdma statistic qp set link mlx5_0/1 auto type on
./rdma_exporter --collector.qp-counters
```

QP dump keys appear as `rdma_qp_*` only after the operator enables the names in the port optional-set (`set` replaces the whole list) and turns QP `optional-counters on` (mlx5, Linux 6.15+), **before** user QPs go INIT. That needs `--collector.qp-counters`. It is not a gate for port-level `rdma_optional_*`. Use the hardware-counters oneshot with `RDMA_ENABLE_QP_COUNTERS=1` (see above) instead of manual `rdma statistic set` / `rdma statistic qp set` on each port.

Port-level `--collector.optional-counters` (default on) emits `_total` for `cc_*` and, when the matching netlink names are enabled and a value was read, `rdma_optional_{rx,tx}_{bytes,packets}_total` from `rdma_{rx,tx}_{bytes,packets}`. These are link-wide mlx5 optional flow counters (Linux 6.15+), not sysfs `port_rcv_data`, not `rdma_qp_*`, and not `rdma_netdev_vport_rdma_*`. Packet and byte counters in the same direction share one hardware flow counter: a newly enabled sibling can include history while the other stays on, and the counter resets only after both names in that direction are disabled. `rdma_optional_counter_enabled` continues to cover every optional name. Port-level `rdma_optional_*` needs only the port optional-set; it does not need `--collector.qp-counters` or QP `optional-counters on`.

The port optional-set and QP `auto` / `optional-counters on` are not persistent. Re-apply both after boot, driver reload, or VF hotplug with the hardware-counters oneshot and udev `SYSTEMD_WANTS` pattern (`After=rdma-hw.target`; do not use udev `RUN+=`). Start the oneshot before RDMA applications.

Some mlx5 QP counters are 32-bit and can wrap. Prefer short `rate()` windows (on the order of the scrape interval) rather than long-range `increase()`.

To print build information without starting the server, add `--version`.

## Configuration

Every CLI flag has an equivalent environment variable. Environment values provide defaults; explicit CLI flags take precedence.


| Flag                              | Environment                                   | Default    | Description                                                                                                                                                      |
| --------------------------------- | --------------------------------------------- | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--listen-address`                | `RDMA_EXPORTER_LISTEN_ADDRESS`                | `:9879`    | HTTP listen address                                                                                                                                              |
| `--metrics-path`                  | `RDMA_EXPORTER_METRICS_PATH`                  | `/metrics` | Metrics endpoint path                                                                                                                                            |
| `--health-path`                   | `RDMA_EXPORTER_HEALTH_PATH`                   | `/healthz` | Health check endpoint path                                                                                                                                       |
| `--log-level`                     | `RDMA_EXPORTER_LOG_LEVEL`                     | `info`     | Log verbosity (`debug`, `info`, `warn`, `error`)                                                                                                                 |
| `--sysfs-root`                    | `RDMA_EXPORTER_SYSFS_ROOT`                    | `/sys`     | Root directory used to read RDMA sysfs data                                                                                                                      |
| `--scrape-timeout`                | `RDMA_EXPORTER_SCRAPE_TIMEOUT`                | `5s`       | Upper bound for metric gathering per scrape                                                                                                                      |
| `--collector.ethtool`             | `RDMA_EXPORTER_COLLECTOR_ETHTOOL`             | `true`     | RoCEv2 PFC and netdev hardware ethtool families (buffer/PCIe/PHY, IEEE 802.3x pause, pause storm, vport RDMA). Disable with `--no-collector.ethtool`.            |
| `--collector.optional-counters`   | `RDMA_EXPORTER_COLLECTOR_OPTIONAL_COUNTERS`   | `true`     | Optional RDMA hardware counters (mlx5 `cc_*` and `rdma_{rx,tx}_{bytes,packets}`) via NETLINK_RDMA. The exporter never turns counters on. Disable with `--no-collector.optional-counters`. |
| `--collector.qp-counters`         | `RDMA_EXPORTER_COLLECTOR_QP_COUNTERS`         | `false`    | Live auto-type QP counters (Linux only, separate NETLINK_RDMA socket). Off by default: the dump can blow the scrape timeout on dense hosts. The exporter never binds QPs or enables auto mode. |
| `--exclude-devices`               | `RDMA_EXPORTER_EXCLUDE_DEVICES`               | ``         | Comma-separated list of RDMA devices to exclude (e.g., `mlx5_0,mlx5_1`)                                                                                          |

Last explicit CLI flag wins (so systemd drop-ins can append `--no-collector.ethtool`). `--collector.X=false` is equivalent to `--no-collector.X`. `--help` / `--version` still work if leftover `RDMA_EXPORTER_ENABLE_*` is set; a normal start refuses those variables with a rename message (`RDMA_EXPORTER_ENABLE_ROCE_PFC_METRICS`, `RDMA_EXPORTER_ENABLE_NETDEV_HW_METRICS`, `RDMA_EXPORTER_ENABLE_RDMA_OPTIONAL_COUNTERS`, `RDMA_EXPORTER_ENABLE_RDMA_QP_COUNTERS`). The matching `--enable-*` flags are also rejected.

## Metrics

- `rdma_<counter>_total{device,port}` – Port and hardware counters aligned with NVIDIA documentation (e.g. `rdma_port_rcv_data_total`, `rdma_symbol_error_total`, `rdma_duplicate_request_total`).
- `rdma_lifespan_milliseconds{device,port}` – Gauge of the sysfs `hw_counters/lifespan` update period in **milliseconds** (kernel default 10, writable range 0–10000). Not a cumulative counter; do not `rate()` it. Replaces the former mis-typed `rdma_lifespan_total`. The exporter does not write this file.
- `rdma_port_info{device,port,netdev,link_layer,state,phys_state,link_width,link_speed,pci_addr,is_vf,pf_device}` – Gauge set to `1` with descriptive labels. `netdev` is the first non-empty kernel interface in sysfs `gid_attrs/ndevs/*` (e.g. `ens1f0np0`); empty when the port has no netdev. Sysfs, optional, and QP counters stay `{device,port}` and do not carry `netdev`. `pci_addr` is the device PCI address (e.g. `0000:1a:00.0`) for joins with external PCI-keyed series (e.g. `sriov_kubepoddevice`); `is_vf` is `"true"` for SR-IOV virtual functions; `pf_device` names the parent PF IB device when `is_vf="true"` (empty otherwise). Adding `netdev` changes the `rdma_port_info` series identity; old series go stale (typically about 5 minutes). Do not `count(rdma_port_info)` across the upgrade without `by (instance, device, port)`.

```promql
rdma_port_rcv_data_total
  * on(instance, device, port) group_left(netdev)
  rdma_port_info
```

A bare `rdma_port_rcv_data_total * rdma_port_info` fails many-to-one matching because the info gauge has extra labels.
- `rdma_scrape_errors_total{}` – Counter incremented when sysfs collection fails.
- `rdma_roce_pfc_pause_frames_total{device,port,netdev,direction,priority}` – RoCEv2 PFC pause frames from ethtool stats.
- `rdma_roce_pfc_pause_duration_total{device,port,netdev,direction,priority}` – Cumulative PFC pause duration in **microseconds**. Occupancy is `rate(...[$interval]) / 1e6`.
- `rdma_roce_pfc_pause_transitions_total{device,port,netdev,direction,priority}` – PFC XOFF-to-XON transitions (mlx5 exposes receive/`rx` only).
- `rdma_roce_pfc_scrape_errors_total{}` – Counter incremented when PFC ethtool collection fails.
- `rdma_scrape_collector_success{collector}` – Gauge `1`/`0` for each enabled collector (`ethtool`, `optional-counters`, `qp-counters`). `0` means init or Prepare failed, or QP counters are unsupported. Absent when that collector is disabled.
- `rdma_netdev_*` / `rdma_pcie_*` / `rdma_phy_*` – Ethtool hardware counters (`--collector.ethtool`, default on). See below.
- `rdma_netdev_scrape_errors_total{}` – Counter incremented when netdev hardware ethtool collection fails.
- `rdma_optional_counter_enabled{device,port,counter}` – Gauge (`1`/`0`) for each optional hardware counter advertised by `rdma statistic mode`. Default on (`--collector.optional-counters`, Linux 5.16+).
- `rdma_optional_counter_scrape_errors_total{}` – Counter incremented when optional-counter netlink collection fails, including an enabled counter whose value was missing in the same scrape.
- `rdma_cc_rx_ce_pkts_total`, `rdma_cc_rx_cnp_pkts_total`, `rdma_cc_tx_cnp_pkts_total` – Optional mlx5 congestion-control counters from RDMA netlink. These never appear in sysfs `hw_counters` (`IB_STAT_FLAG_OPTIONAL`). They are distinct from the always-on NP/RP counters (`rdma_np_*`, `rdma_rp_*`). Values are emitted only while the counter is enabled and a value was read.
- `rdma_optional_rx_bytes_total`, `rdma_optional_tx_bytes_total`, `rdma_optional_rx_packets_total`, `rdma_optional_tx_packets_total` – Link-wide mlx5 RDMA octets/packets from the enabled optional flow counter (netlink `rdma_{rx,tx}_{bytes,packets}`). Not sysfs `port_rcv_data` (doublewords). Not `rdma_qp_*`. Not `rdma_netdev_vport_rdma_*`. Same-direction packet and byte names share one flow counter (history can predate a newly enabled sibling; reset only after both are disabled). Status discovery needs Linux 5.16+; mlx5 added these names in Linux 6.15. Other unmapped optional names still appear only on `rdma_optional_counter_enabled`.

```
# TYPE rdma_optional_rx_bytes_total counter
rdma_optional_rx_bytes_total{device="mlx5_0",port="1"} 123456
# TYPE rdma_optional_rx_packets_total counter
rdma_optional_rx_packets_total{device="mlx5_0",port="1"} 789
```

- `rdma_qp_counter_mode{device,port,mode}` / `rdma_qp_auto_mask{device,port,criteria}` – Port-level QP bind mode and auto mask (`none|auto|manual`, `type|pid`). Requires `--collector.qp-counters`. Mode gauges are emitted even when the dump is skipped (not auto type-only). Empty dumps still emit scrape status when a type-only auto dump ran and no user QP has gone INIT yet.
- `rdma_qp_scrape_status{device,port,result}` – `ok`, `overflow`, or `error` for the last QP dump on that port. Omitted when the dump is skipped (mode is not auto type-only). Overflow drops totals for that port only; other ports, `cc_*`, and `rdma_optional_*` continue.
- `rdma_qp_scrape_errors_total{}` – Counter incremented on QP netlink failures, including dump overflow.
- `rdma_qp_<name>_total{device,port,qp_type}` – Live auto-type bound user QP aggregate (`duplicate_request`, `implied_nak_seq_err`, `local_ack_timeout_err`, `packet_seq_err`, `rnr_nak_retry_err`, `out_of_buffer`, `rx_write_requests`, `rx_read_requests`, `rx_atomic_requests`, plus optional `rx_bytes` / `tx_bytes` / `rx_packets` / `tx_packets` from dump keys `rdma_*`). Not per-LQPN. Do not add these to the same-named sysfs `rdma_<name>_total` series. Optional traffic series are omitted unless the dump contains those keys; dump key `rdma_rx_bytes` becomes `rdma_qp_rx_bytes_total`, not `rdma_qp_rdma_rx_bytes_total`.

See the [Run](#run) section for operator enablement (`rdma statistic set` / `rdma statistic qp set auto type on`).

The Go and process collectors from `client_golang` are registered automatically.

PFC and ethtool hardware metrics are collected for Ethernet ports that are not flagged as PCI VFs. That is not a positive PF guarantee. A BDF without `physfn` still gets PFC and buffer/PCIe/PHY families. Physical-port counters on a PF include VF traffic. vPort RDMA is the scraped netdev's function vport, not those physical-port totals, and is omitted when `sriov_totalvfs` is absent. Series are emitted only for counters the driver actually returns; missing priorities do not mean “zero pauses”.

### PFC direction

These are observations, not root-cause labels:

- `direction="rx"`: the peer XOFFed this NIC, so this NIC cannot transmit on that priority.
- `direction="tx"`: this NIC XOFFed the peer because it is not absorbing that priority.

Treat a sustained `rx` pause as a reason to inspect the network path, and a sustained `tx` pause as a reason to inspect NIC receive buffers, PCIe, and the host. Do not read `tx` pause as switch congestion.

Pause occupancy:

```promql
rate(rdma_roce_pfc_pause_duration_total[$interval]) / 1e6
```

### Netdev hardware counters

These families ship with `--collector.ethtool` (default on). Disable with `--no-collector.ethtool`. They are mlx5 netdev/device statistics correlated to an RDMA port via `netdev`; they are not RoCE-only.

- Buffer/drop: `rdma_netdev_prio_buf_discard_total`, `rdma_netdev_prio_cong_discard_total`, `rdma_netdev_prio_discards_total`, `rdma_netdev_prio_ecn_marked_total`, `rdma_netdev_dev_out_of_buffer_total`, `rdma_netdev_rx_out_of_buffer_total`, `rdma_netdev_rx_discards_phy_total`. Distinct from the sysfs QP WQE counter `rdma_out_of_buffer_total`.
- PCIe: `rdma_pcie_outbound_stalled_percent` is a **gauge** of the last 1 second (kernel 0–100) and can miss stalls shorter than the scrape interval. Alert on `rate(rdma_pcie_outbound_stalled_seconds_total[$interval])`, the fraction of time stall exceeded 30%. Also `rdma_pcie_outbound_buffer_overflow_total` and `rdma_pcie_signal_integrity_total`.
- PHY/FEC: `rdma_phy_rx_corrected_bits_total`, `rdma_phy_rx_pcs_symbol_err_total`, `rdma_phy_rx_bits_total`, `rdma_phy_rx_err_lane_total`, `rdma_phy_rx_crc_errors_total`, `rdma_phy_link_down_events_total`.
- IEEE 802.3x global pause (not PFC; keys exist only when global pause mode is on): `rdma_netdev_global_pause_frames_total{device,port,netdev,direction}`, `rdma_netdev_global_pause_duration_total` (microseconds; occupancy is `rate()/1e6`), `rdma_netdev_global_pause_transitions_total` (mlx5 receive/`rx` only). Direction is observation only: `rx` means this NIC received a pause request (was asked to stop transmitting); `tx` means this NIC transmitted a pause request (asked the peer to stop). Do not treat as root cause.
- Pause storm: `rdma_netdev_pause_storm_events_total{device,port,netdev,severity}`. `warning` is stalled past a watermark; `error` is timeout and pause TX disabled (drops may have occurred). Observation only; do not assert root cause.
- vPort RDMA: `rdma_netdev_vport_rdma_bytes_total` and `rdma_netdev_vport_rdma_packets_total` labeled `{device,port,netdev,direction,traffic}` from `{rx,tx}_vport_rdma_{unicast,multicast}_{bytes,packets}`. These are octets/packets steered to or from this netdev's function vport, not `*_phy` and not a sum of other function vports. Do not add them to sysfs `port_rcv_data` (doublewords) or `rdma_qp_{rx,tx}_{bytes,packets}_total`. Ethernet vport, loopback, and steer-miss keys are not exported. Series are omitted when the RDMA device is not a PCI BDF, when `/sys/bus/pci/devices/<bdf>/sriov_totalvfs` is absent (the file's value is ignored), when more than one Ethernet `(device,port)` shares the netdev, or when `phys_port_name` is a VF/SF/host-PF representor (`pf0vf1`, `c1pf0vf0`, `pf0hpf`). A missing `phys_port_name` does not omit. Host VFs with `physfn` still skip all ethtool families. `IsVF` stays fail-open: a BDF without `physfn` still gets PFC and other HW families.

Interval FEC/BER ratio (not a lifetime or instantaneous instrument):

```promql
increase(rdma_phy_rx_corrected_bits_total[$interval])
  /
clamp_min(increase(rdma_phy_rx_bits_total[$interval]), 1)
```

Use the same form with `rdma_phy_rx_pcs_symbol_err_total` for uncorrected symbol errors.

These counters add one series per present allowlisted ethtool key (not a full `ethtool -S` dump). Disable with `--no-collector.ethtool`.

### RoCEv2 congestion control

Default sysfs `hw_counters` already export Notification Point / Reaction Point counters (`rdma_np_cnp_sent_total`, `rdma_np_ecn_marked_roce_packets_total`, `rdma_rp_cnp_handled_total`, `rdma_rp_cnp_ignored_total`). mlx5 optional counters (`cc_rx_ce_pkts`, `cc_rx_cnp_pkts`, `cc_tx_cnp_pkts`, and `rdma_{rx,tx}_{bytes,packets}`) are **not** in sysfs even when enabled with `rdma statistic set`; scrape them with `--collector.optional-counters` (default on) as described in [Run](#run). Do not add `rdma_optional_*` to `rdma_qp_*` or to sysfs `port_rcv_data`.

## Dashboards

- Bundled JSON: [`dashboards/rdma_exporter_dashboard.json`](dashboards/rdma_exporter_dashboard.json) includes PFC occupancy, IEEE 802.3x pause occupancy/frames/transitions, pause storm, optional CC, port-level optional traffic, PCIe stall, PHY/FEC, and QP optional traffic panels. PFC/PCIe/PHY/pause panels need `--collector.ethtool` (default on). Optional CC and port-level `rdma_optional_*` need `--collector.optional-counters` (default on). QP traffic panels need `--collector.qp-counters` and appear only when the dump contains those keys.
- Grafana.com: [RDMA/RoCE NIC Telemetry](https://grafana.com/grafana/dashboards/24241-rdma-roce-nic-telemetry/) – community copy; updating that listing is a separate publish step.

## Testing

```bash
go test ./...
```

For deterministic builds in shared environments, you can pin Go's caches locally:

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./...
```

`internal/rdma/testdata/sysfs` contains fixture trees used in unit tests to emulate sysfs layouts.

## Deployment

- systemd unit files: [`deploy/systemd/rdma_exporter.service`](deploy/systemd/rdma_exporter.service), [`deploy/systemd/rdma-hardware-counters.service`](deploy/systemd/rdma-hardware-counters.service).
- Hardware counter enablement (optional and QP auto mode): [`deploy/scripts/rdma-enable-hardware-counters.sh`](deploy/scripts/rdma-enable-hardware-counters.sh), [`deploy/systemd/rdma-hardware-counters.env.example`](deploy/systemd/rdma-hardware-counters.env.example), [`deploy/systemd/rdma_exporter-qp-counters.conf.example`](deploy/systemd/rdma_exporter-qp-counters.conf.example), [`deploy/udev/90-rdma-hardware-counters.rules`](deploy/udev/90-rdma-hardware-counters.rules).
- A multi-stage Dockerfile lives at the repository root; see `docs/deployment.md` for build and run instructions.

## Development Notes

- Architectural decisions and future work are documented in `docs/design.md`.
- Logging uses the Go standard library `log/slog`. Set `--log-level=debug` for detailed scrape traces.
- Deployment guidance (systemd and container) lives in `docs/deployment.md`.

## License

This project is licensed under the MIT License. See `LICENSE` for full text.
