//go:build !unix

package main

import "errors"

func restartProcess() error { return errors.New("当前平台不支持自重启") }
