package updater

import "testing"

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"v1.2.3", "1.2.3", 0},
		{"V1.2.3", "1.2.3", 0},
		{"1.9.9", "1.10.0", -1},
		{"1.2", "1.2.0", 0},
		{"2", "1.9.9", 1},
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-rc1", "1.2.3-rc2", -1},
		{"1.2.3-alpha", "1.2.3-alpha.1", -1},
		{"1.2.3-1", "1.2.3-a", -1},   // 数字段小于字母段
		{"1.2.3+build5", "1.2.3", 0}, // 忽略 build 元数据
		{"0.0.1", "0.0.1-dev", 1},    // 预发布小于正式
	}
	for _, c := range cases {
		av, err := parseVersion(c.a)
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", c.a, err)
		}
		bv, err := parseVersion(c.b)
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", c.b, err)
		}
		if got := compareVersion(av, bv); got != c.want {
			t.Fatalf("compare(%q,%q)=%d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, s := range []string{"", "dev", "latest", "1.2.3.4", "1.x.3", "-1.2.3", "1.2.3-"} {
		if _, err := parseVersion(s); err == nil {
			t.Fatalf("parseVersion(%q) 应该报错", s)
		}
	}
}

func TestParseLocalFallsBack(t *testing.T) {
	// 本机版本解析不了时（dev / 空 / 非法）视为最低，永远提示可升级。
	for _, s := range []string{"", "dev", "unknown"} {
		v := parseLocal(s)
		if v.Major != 0 || v.Minor != 0 || v.Patch != 0 {
			t.Fatalf("parseLocal(%q) = %+v，期望 0.0.0", s, v)
		}
		if len(v.Pre) == 0 {
			t.Fatalf("parseLocal(%q) 应该是预发布版本", s)
		}
		if compareVersion(v, mustParse(t, "0.0.1-dev")) >= 0 {
			t.Fatalf("parseLocal(%q) 应该低于 0.0.1-dev", s)
		}
	}
	// 正常版本原样解析。
	if v := parseLocal("v1.4.0"); v.Major != 1 || v.Minor != 4 || v.Patch != 0 {
		t.Fatalf("parseLocal(v1.4.0) = %+v", v)
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v1.2.3":        "1.2.3",
		"1.2.3":         "1.2.3",
		"1.2.3+build7":  "1.2.3",
		"v1.2.3-rc1+b":  "1.2.3-rc1",
		"  v1.2.3  ":    "1.2.3",
		"V1.2.3-RC1":    "1.2.3-RC1",
		"not-a-version": "not-a-version",
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Fatalf("normalizeVersion(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := parseVersion(s)
	if err != nil {
		t.Fatalf("解析 %q 失败: %v", s, err)
	}
	return v
}
