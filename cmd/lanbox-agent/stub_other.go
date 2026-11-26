//go:build !windows

// Command lanbox-agent 仅在 Windows 上构建。
// 这个文件让 `go build ./...` / `go vet ./...` 在其它平台上仍能通过。
package main

import "fmt"

func main() {
	fmt.Println("lanbox-agent 仅支持 Windows")
}
