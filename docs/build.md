# make 编译与试跑手册

项目需要 Go 1.27+ 和 GNU make。输出为单个 `rdma_exporter` 二进制，下面的构建关闭 CGO。

## Windows Git Bash：编译给 H100 节点使用

在 Git Bash 中进入项目目录，设置当前终端的缓存和目标平台：

```bash
cd /c/Users/DELL/Desktop/workspace/code/rdma_exporter

export GOCACHE="$(cygpath -m "$PWD")/.gocache"
export GOMODCACHE="$(cygpath -m "$PWD")/.gomodcache"
export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64

make build
file rdma_exporter
```

`cygpath -m` 把 Git Bash 路径转换成 Windows Go 可识别的绝对路径。以上 export 只影响当前终端；新开终端需重新设置。
`h100-node25` 是 x86_64，对应 `GOARCH=amd64`。`file` 应显示 `ELF 64-bit`、`x86-64`、`statically linked`。
如果显示 `PE32`，则编译成了 Windows 程序，需要检查 GOOS 后强制重新构建。

生成文件在项目根目录，名为 `rdma_exporter`，没有 `.exe` 后缀，直接拷到 Linux 即可；不要在 Windows 上执行它。

当前 Makefile 的二进制目标没有源码依赖列表：文件已经存在时，`make build` 会认为无需更新，即使源码发生变化。
修改源码、切换架构或更换构建参数后，使用：

```bash
make -B build
```

若需要 ARM64，在以上终端中执行：

```bash
GOARCH=arm64 make -B build
```

这会覆盖同名输出；应先保存上一架构的二进制。需要压缩包时可将当前产物打包到忽略的 bin 目录：

```bash
mkdir -p bin
tar -czf bin/rdma_exporter-linux-amd64.tar.gz rdma_exporter
```

打包 ARM64 产物时将压缩包名称中的 amd64 改为 arm64。

## Linux 本机编译

在 Linux 项目目录执行，架构默认跟随本机：

```bash
export GOCACHE="$PWD/.gocache"
export GOMODCACHE="$PWD/.gomodcache"
CGO_ENABLED=0 make -B build
```

## make 命令说明与测试

| 命令 | 作用 |
| --- | --- |
| `make` / `make all` / `make build` | 构建项目根目录的 rdma_exporter；输出已存在时可能跳过 |
| `make -B build` | 强制重新构建 |
| `make test` | 执行 `go test ./...` |
| `make lint` | 执行 `go vet ./...`，不修改源码 |
| `make fmt` | 使用 gofmt 修改 Go 源码格式 |
| `make grafana-com-export` | 重新生成 Grafana.com 导出文件，与本次二进制构建无关 |
| `make clean` | 删除本地生成的 rdma_exporter |

完整测试应在 Linux 环境执行：已有 sysfs fixture 包含 Linux PCI 路径、符号链接和权限语义。
Windows Git Bash 导出的 GOOS=linux 用于交叉编译，不能使 Windows Go 直接执行 Linux 测试二进制。

在 Linux 项目目录设置上述缓存变量后执行：

```bash
make test
make lint
CGO_ENABLED=1 go test -race ./...
```

race 检查还需要可用的 C 编译器，例如 gcc；静态二进制构建本身不需要它。

## 放到目标机器试跑

将产物放到例如 `/root/rdma_exporter-ops`，在目标 Linux 机器执行：

```bash
chmod +x /root/rdma_exporter-ops
command -v ibv_devinfo
/root/rdma_exporter-ops --listen-address=127.0.0.1:19879
```

该命令前台运行，Ctrl+C 停止。另开终端查看新增指标：

```bash
curl -fsS http://127.0.0.1:19879/metrics |
  grep -E '^rdma_(netdev_mtu_bytes|port_active_mtu_bytes|device_info|driver_info|gid_info|port_mtu_scrape_errors_total)'
```

五类新增指标默认开启。active_mtu 需要 PATH 中有可运行的 ibv_devinfo；缺失时只省略端口 MTU，启动日志会提示。
可用 `--no-collector.port-mtu` 单独关闭查询。sysfs 数据仍正常采集，详细语义见 [README 运维元数据](../README.md#rdma-运维元数据)。

现场参考：reth0–7 网卡 MTU 为 4200，mlx5_0–7/1 active_mtu 为 4096；reth8 MTU 为 1500，mlx5_8/1 active_mtu 为 1024。
固件分别为 28.41.1000 和 20.42.1000，驱动版本为 23.10-2.1.3，当前有效 GID 共 36 条。
