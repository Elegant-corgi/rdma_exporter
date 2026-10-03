# Deployment

This guide explains two supported deployment options for `rdma_exporter`: running it as a systemd service on a Linux host, and running it from a container image built with the provided Dockerfile.

## Prerequisites

- A Linux host with RDMA devices accessible through `/sys/class/infiniband`.
- systemd 235 或更新版本（使用动态用户及自动创建的状态目录）。
- Prometheus or another monitoring system configured to scrape the exporter.

## systemd service

目标节点只需复制与 Linux/CPU 架构匹配的静态二进制和 `rdma_exporter.service`，无需源码、Go 环境或必需的配置文件。构建节点可使用以下命令生成 Linux amd64 二进制（arm64 节点改为 `GOARCH=arm64`）：

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rdma_exporter .
```

将两个文件放到目标节点的同一目录后执行：

1. **安装二进制和 service 文件**
   ```bash
   sudo install -Dm0755 rdma_exporter /usr/local/exporters/rdma_exporter/rdma_exporter
   sudo install -Dm0644 rdma_exporter.service /etc/systemd/system/rdma_exporter.service
   ```
   若直接从仓库安装，service 文件源路径为 `deploy/systemd/rdma_exporter.service`。

2. **启动并设置开机自启**
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now rdma_exporter.service
   ```

3. **验证服务**
   ```bash
   systemctl status rdma_exporter.service
   curl -f http://localhost:19879/healthz
   curl -f http://localhost:19879/metrics
   journalctl -u rdma_exporter.service -n 50 --no-pager
   ```

service 通过 `--listen-address=:19879` 监听所有网卡，Prometheus 抓取地址为 `http://<节点IP>:19879/metrics`；节点防火墙需允许 Prometheus 访问此端口。
`DynamicUser=true` 由 systemd 管理非特权用户，无需执行 `useradd`；状态目录由 `StateDirectory=` 自动创建。
保留原有只读文件系统及能力限制，exporter 不写 sysfs，不启用硬件计数器，不绑定 QP。

`/etc/rdma_exporter.env` 可选；缺失不影响启动。需要时可在其中配置 `RDMA_EXPORTER_LOG_LEVEL=info` 等环境变量。
监听端口由 service 的 CLI 参数确定，环境变量不能覆盖；修改端口需修改 `ExecStart`。
不要保留已移除的 `RDMA_EXPORTER_ENABLE_*` 变量，否则启动失败并反复重启。
Ethtool 和 optional-counters 默认开启，QP dump 默认关闭。

仅部署二进制即可启动基础采集。端口 `active_mtu` 查询额外需要节点已有 `ibv_devinfo`、其运行依赖和设备访问权限；工具缺失时会警告并停用该查询，其他采集继续。
如不需要此指标，可在可选环境文件中设置 `RDMA_EXPORTER_COLLECTOR_PORT_MTU=false`。采集完整性仍需在目标 RDMA 节点核验。

## Docker image

The repository includes a multi-stage Dockerfile at the repository root.

1. **Build the image**
   ```bash
   docker build -t rdma-exporter:latest .
   ```

2. **Run the exporter**
   ```bash
   docker run --rm \
     --name rdma-exporter \
     --network host \
     --read-only \
     -v /sys/class/infiniband:/sys/class/infiniband:ro \
     -e RDMA_EXPORTER_LOG_LEVEL=info \
     rdma-exporter:latest
   ```

   `--network host` keeps the default listen address and allows NETLINK_RDMA optional-counter scrapes to reach the host RDMA subsystem. Alternatively, expose port `9879/tcp` explicitly with `-p 9879:9879` and set `--listen-address=:9879` or another value fitting your environment.

3. **Persist configuration** (optional)

   For custom environments, mount an environment file:
   ```bash
   docker run --rm \
     --env-file ./rdma_exporter.env \
     --network host \
     -v /sys/class/infiniband:/sys/class/infiniband:ro \
     rdma-exporter:latest
   ```

The image runs as the unprivileged `rdma_exporter` user by default and contains only the exporter binary plus CA certificates.
