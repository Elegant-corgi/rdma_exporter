//go:build linux

package portmtu

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func New() (*Provider, error) {
	path, err := exec.LookPath("ibv_devinfo")
	if err != nil {
		return nil, fmt.Errorf("定位 ibv_devinfo: %w", err)
	}
	return &Provider{path: path, run: runCommand}, nil
}

type boundedOutput struct {
	mu       sync.Mutex
	data     bytes.Buffer
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	remaining := outputLimit - b.data.Len()
	if n > remaining {
		data = data[:remaining]
		b.overflow = true
		b.cancel()
	}
	_, _ = b.data.Write(data)
	return n, nil
}

func runCommand(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	output := &boundedOutput{cancel: cancel}
	cmd := exec.CommandContext(ctx, path, args...)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "LC_ALL=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if output.overflow {
		return nil, fmt.Errorf("ibv_devinfo 输出超过 64 KiB")
	}
	if err != nil {
		return nil, fmt.Errorf("执行 ibv_devinfo: %w", err)
	}
	return output.data.Bytes(), nil
}
