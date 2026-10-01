//go:build linux

package portmtu

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 测试进程充当命令，覆盖真实管道、取消和内存上限。
func TestCommandHelper(t *testing.T) {
	mode := os.Getenv("RDMA_MTU_TEST_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "ok":
		if os.Getenv("LC_ALL") != "C" {
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString("port: 1\nactive_mtu: 1024 (3)\n")
	case "fail":
		_, _ = os.Stdout.WriteString("port: 1\nactive_mtu: 1024 (3)\n")
		os.Exit(3)
	case "large":
		for {
			_, _ = os.Stdout.WriteString(strings.Repeat("x", 4096))
		}
	case "hang":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestRunCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ok", "fail", "large", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("RDMA_MTU_TEST_HELPER", mode)
			t.Setenv("GORACE", "atexit_sleep_ms=0")
			timeout := 2 * time.Second
			if mode == "hang" {
				timeout = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			start := time.Now()
			data, err := runCommand(ctx, exe, "-test.run=^TestCommandHelper$")
			if (err != nil) != (mode != "ok") {
				t.Fatalf("err=%v", err)
			}
			if len(data) > outputLimit {
				t.Fatal("输出未限制")
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("命令未及时终止")
			}
		})
	}
}

func TestMissingCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := New(); err == nil {
		t.Fatal("应报告工具缺失")
	}
}
