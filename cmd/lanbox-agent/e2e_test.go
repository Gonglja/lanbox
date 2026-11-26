//go:build e2e && windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lanbox/internal/agent"
	"lanbox/internal/client"
)

// TestAgentE2E 对真实服务端跑一遍登录 → 剪贴板文本上传 → 文件夹上传 → 撤回。
//
//	LANBOX_E2E_URL=https://127.0.0.1:18443 LANBOX_E2E_PASSWORD=... \
//	  go test -tags e2e ./cmd/lanbox-agent -run TestAgentE2E -v
func TestAgentE2E(t *testing.T) {
	base := os.Getenv("LANBOX_E2E_URL")
	pass := os.Getenv("LANBOX_E2E_PASSWORD")
	if base == "" || pass == "" {
		t.Skip("未设置 LANBOX_E2E_URL / LANBOX_E2E_PASSWORD")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fp, err := probeCert(ctx, base)
	if err != nil {
		t.Fatalf("获取证书指纹失败: %v", err)
	}
	t.Logf("服务端证书指纹: %s", fp)

	sess, hc, err := doLogin(ctx, base, "admin", pass, fp)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	cli, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: hc})
	if err != nil {
		t.Fatal(err)
	}

	holder := &clientHolder{}
	holder.set(cli)
	settings := defaultSettings()
	records := make(chan []agent.Record, 16)
	mgr := agent.NewManager(agent.Options{
		Uploader:    &uploader{holder: holder},
		Settings:    settings.agentSettings(),
		HistorySize: 20,
		Classify:    classify,
		OnRecords:   func(r []agent.Record) { select { case records <- r: default: } },
		Logger:      t.Logf,
		Debounce:    -1,
	})
	defer mgr.Close()
	mgr.SetAvailable(true)

	// 1) 文本
	mgr.Submit(agent.Clip{Text: "e2e hello from clipboard"})
	rec := waitRecord(t, records, func(r agent.Record) bool { return r.State == agent.StateOK })
	if rec.ItemID == "" {
		t.Fatal("文本上传没有返回 item id")
	}
	item, err := cli.GetItem(ctx, rec.ItemID)
	if err != nil {
		t.Fatalf("读取服务端内容失败: %v", err)
	}
	if item.Text != "e2e hello from clipboard" {
		t.Fatalf("服务端文本不一致: %q", item.Text)
	}

	// 2) 文件夹
	dir := t.TempDir()
	root := filepath.Join(dir, "素材")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("bbbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SubmitManual("", []string{root}); err != nil {
		t.Fatalf("目录上传失败: %v", err)
	}
	rec2 := waitRecord(t, records, func(r agent.Record) bool {
		return r.State == agent.StateOK && r.Kind == agent.KindFiles
	})
	item2, err := cli.GetItem(ctx, rec2.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item2.Files) != 2 {
		t.Fatalf("应有 2 个附件，实际 %d", len(item2.Files))
	}
	paths := map[string]bool{}
	for _, f := range item2.Files {
		paths[f.RelPath()] = true
	}
	if !paths["素材/a.txt"] || !paths["素材/sub/b.txt"] {
		t.Fatalf("附件相对路径不符合预期: %v", paths)
	}

	// 3) 撤回
	if err := mgr.Withdraw(ctx, rec2.ID); err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if _, err := cli.GetItem(ctx, rec2.ItemID); err == nil {
		t.Fatal("撤回后服务端内容仍存在")
	}
	t.Logf("e2e 通过：文本 + 目录上传 + 撤回")
}

func waitRecord(t *testing.T, ch <-chan []agent.Record, pred func(agent.Record) bool) agent.Record {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case recs := <-ch:
			for _, r := range recs {
				if pred(r) {
					return r
				}
				if r.State == agent.StateFailed {
					t.Fatalf("上传失败: %s", r.Error)
				}
			}
		case <-deadline:
			t.Fatal("等待上传结果超时")
		}
	}
}
