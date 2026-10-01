package portmtu

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int
		bad         bool
	}{
		{"双端口", "hca_id: mlx5_8\n port: 1\n active_mtu: 1024 (3)\n port: 2\n active_mtu: 4096 (5)\n", 2, false},
		{"空", "", 0, true}, {"缺字段", "port: 1\n", 0, true},
		{"缺端口", "active_mtu: 1024 (3)", 0, true},
		{"错误枚举", "port: 1\nactive_mtu: 1500 (3)", 0, true},
		{"重复端口", "port: 1\nactive_mtu: 1024 (3)\nport: 1", 0, true},
		{"重复MTU", "port: 1\nactive_mtu: 1024 (3)\nactive_mtu: 1024 (3)", 0, true},
		{"部分数据", "port: 1\nactive_mtu: 1024 (3)\nport: 2", 0, true},
		{"负端口", "port: -1\nactive_mtu: 1024 (3)", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parse([]byte(tc.input))
			if (err != nil) != tc.bad || len(result) != tc.want {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if !tc.bad && (result[1] != 1024 || result[2] != 4096) {
				t.Fatal(result)
			}
		})
	}
}

func TestProviderBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    int
		failure bool
	}{{"正常", 0, false}, {"命令失败", 0, true}, {"输出超限", outputLimit + 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{path: "/tool", run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > time.Second {
					t.Fatal("缺少查询超时")
				}
				if path != "/tool" || strings.Join(args, " ") != "-d mlx5_8" {
					t.Fatal(path, args)
				}
				if tc.failure {
					return []byte("port: 1\nactive_mtu: 1024 (3)"), errors.New("exit 1")
				}
				if tc.size > 0 {
					return make([]byte, tc.size), nil
				}
				return []byte("port: 1\nactive_mtu: 1024 (3)"), nil
			}}
			result, err := p.MTUs(context.Background(), "mlx5_8")
			if (err != nil) != (tc.failure || tc.size > 0) {
				t.Fatal(err)
			}
			if err != nil && result != nil {
				t.Fatal("失败后导出了部分结果")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Provider{run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("取消后不应执行")
		return nil, nil
	}}
	if _, err := p.MTUs(ctx, "mlx5_8"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	if _, err := p.MTUs(ctx, "mlx5_8"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
