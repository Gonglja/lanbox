//go:build !android

// 非 Android 平台留一个空 main，保证 go build ./... / go vet ./... 在开发机上可跑。
package main

func main() {}
