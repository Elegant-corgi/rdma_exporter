//go:build !linux

package portmtu

import "errors"

func New() (*Provider, error) { return nil, errors.New("端口 MTU 查询仅支持 Linux") }
