package portmtu

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const outputLimit = 64 * 1024

type commandRunner func(context.Context, string, ...string) ([]byte, error)

// Provider 只查询端口属性，不修改设备或创建 QP。
type Provider struct {
	path string
	run  commandRunner
}

func (p *Provider) MTUs(ctx context.Context, device string) (map[int]uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := p.run(ctx, p.path, "-d", device)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > outputLimit {
		return nil, fmt.Errorf("ibv_devinfo 输出超过 64 KiB")
	}
	return parse(data)
}

var mtuPattern = regexp.MustCompile(`^active_mtu:\s*([0-9]+)\s+\(([1-5])\)$`)

func parse(data []byte) (map[int]uint64, error) {
	result := make(map[int]uint64)
	ports := make(map[int]bool)
	current := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "port:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "port:")))
			if err != nil || n <= 0 || ports[n] {
				return nil, fmt.Errorf("无效或重复端口 %q", line)
			}
			current = n
			ports[n] = true
		} else if strings.HasPrefix(line, "active_mtu:") {
			m := mtuPattern.FindStringSubmatch(line)
			if current == 0 || m == nil {
				return nil, fmt.Errorf("无效 active_mtu %q", line)
			}
			value, _ := strconv.ParseUint(m[1], 10, 64)
			enum, _ := strconv.Atoi(m[2])
			if value != uint64(128<<enum) || result[current] != 0 {
				return nil, fmt.Errorf("无效或重复 active_mtu %q", line)
			}
			result[current] = value
		}
	}
	if len(ports) == 0 || len(ports) != len(result) {
		return nil, fmt.Errorf("ibv_devinfo 缺少端口或 active_mtu")
	}
	return result, nil
}
