package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveUpdateHotAndRestartFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":`+jsonPath(dir)+`}`)

	var gotPrev, gotNext Config
	applied := 0
	l, err := OpenLive(cfgPath, Hooks{Apply: func(prev, next Config) error {
		gotPrev, gotNext = prev, next
		applied++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	desired := l.Current()
	desired.MaxTextBytes = 2048
	desired.Listen = ":9443"
	res, err := l.Update(desired)
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if applied != 1 || gotNext.MaxTextBytes != 2048 || gotPrev.MaxTextBytes == gotNext.MaxTextBytes {
		t.Fatalf("热更钩子未收到正确 prev/next: %+v -> %+v", gotPrev, gotNext)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "max_text_bytes" {
		t.Fatalf("applied 不对: %+v", res.Applied)
	}
	if len(res.RestartRequired) != 1 || res.RestartRequired[0] != "listen" {
		t.Fatalf("restart_required 不对: %+v", res.RestartRequired)
	}
	if l.Current().Origins["listen"] != "web" || l.Current().Origins["max_text_bytes"] != "web" {
		t.Fatalf("来源应标为 web: %+v", l.Current().Origins)
	}

	// 清掉覆盖后应回到基线
	if _, err := l.DropWebOverride("listen"); err != nil {
		t.Fatal(err)
	}
	if l.Current().Listen != ":8443" {
		t.Fatalf("DropWebOverride 后 listen 应回到默认: %s", l.Current().Listen)
	}
}

func TestLiveRejectsInvalidValuesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":`+jsonPath(dir)+`}`)
	l, err := OpenLive(cfgPath, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	bad := l.Current()
	bad.MaxFilesPerItem = 0
	if _, err := l.Update(bad); err == nil {
		t.Fatal("非法值应报错")
	}
	if _, err := os.Stat(filepath.Join(dir, "webconfig.json")); !os.IsNotExist(err) {
		t.Fatal("校验失败不应落盘")
	}
}

func TestLiveRestartCooldown(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":`+jsonPath(dir)+`}`)
	calls := 0
	now := time.Now()
	l, err := OpenLive(cfgPath, Hooks{Restart: func() { calls++ }})
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return now }
	l.cooldown = 10 * time.Second

	if !l.RequestRestart() {
		t.Fatal("首次重启应允许")
	}
	if l.RequestRestart() {
		t.Fatal("冷却期内应拒绝")
	}
	now = now.Add(11 * time.Second)
	if !l.RequestRestart() {
		t.Fatal("冷却结束后应允许")
	}
	if calls != 2 {
		t.Fatalf("Restart 钩子应调用 2 次，得到 %d", calls)
	}
}
