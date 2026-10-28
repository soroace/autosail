package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"autosail/internal/aws"
)

func TestNormalizeRegion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "az-suffix", in: "us-east-1a", want: "us-east-1"},
		{name: "single-letter", in: "b", want: "us-east-1"},
		{name: "unchanged", in: "ap-southeast-1", want: "ap-southeast-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeRegion(tc.in); got != tc.want {
				t.Fatalf("normalizeRegion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func parseTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"regionLabel": regionLabel,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatalf("解析模板失败: %v", err)
	}
	return tmpl
}

// 状态徽章由 stateBadge 片段输出，两个实例列表都应正常渲染
func TestTemplatesRenderInstances(t *testing.T) {
	tmpl := parseTemplates(t)

	cases := []struct {
		name string
		data PageData
	}{
		{
			name: "lightsail",
			data: PageData{
				Title: "AutoSail", Username: "tester", Tab: "manage", HasCreds: true, ManageService: "lightsail",
				Instances: []aws.InstanceView{{Name: "vps-running", State: "running"}, {Name: "vps-pending", State: "pending"}},
			},
		},
		{
			name: "ec2",
			data: PageData{
				Title: "AutoSail", Username: "tester", Tab: "manage", HasCreds: true, ManageService: "ec2",
				EC2Instances: []aws.EC2InstanceView{{ID: "i-1", State: "running"}, {ID: "i-2", State: "pending"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "layout", tc.data); err != nil {
				t.Fatalf("渲染 layout 失败: %v", err)
			}
			out := buf.String()
			// 徽章必须带 data-state（静默刷新依赖）且两种状态都渲染出来
			for _, want := range []string{`data-state="running"`, `data-state="pending"`, "● Running", "○ pending"} {
				if !strings.Contains(out, want) {
					t.Fatalf("渲染结果缺少 %q", want)
				}
			}
		})
	}
}

// 轮询间隔必须由服务端按缓存时长注入，避免前端另写一份硬编码
func TestLoginTemplateRenders(t *testing.T) {
	tmpl := parseTemplates(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "login", PageData{Title: "AutoSail"}); err != nil {
		t.Fatalf("渲染 login 失败: %v", err)
	}
}

// 界面只展示 5 个常用规格（过滤 Windows / IPv6 专用 / 超大规格），顺序固定
func TestPickStandardBundles(t *testing.T) {
	list := []aws.BundleView{
		{ID: "large_3_2", TypeName: "large"},
		{ID: "nano_3_2", TypeName: "nano"},
		{ID: "micro_win_3_2", TypeName: "micro", Windows: true},
		{ID: "micro_3_2", TypeName: "micro"},
		{ID: "medium_3_2", TypeName: "medium"},
		{ID: "small_3_2", TypeName: "small"},
		{ID: "nano_ipv6_3_2", TypeName: "nano", IPv6: true},
		{ID: "xlarge_3_2", TypeName: "xlarge"},
	}

	got := pickStandardBundles(list)
	want := []string{"nano_3_2", "micro_3_2", "small_3_2", "medium_3_2", "large_3_2"}
	if len(got) != len(want) {
		t.Fatalf("应返回 %d 个常用规格，实际 %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("第 %d 项应为 %s，实际 %s", i+1, want[i], got[i].ID)
		}
	}
}

// 仅 IPv6 时按基础名找到该区域的 IPv6 专用套餐
func TestIpv6BundleID(t *testing.T) {
	list := []aws.BundleView{
		{ID: "micro_3_2", Base: "micro", TypeName: "micro"},
		{ID: "micro_ipv6_3_2", Base: "micro_ipv6", TypeName: "micro", IPv6: true},
		{ID: "nano_ipv6_3_1", Base: "nano_ipv6", TypeName: "nano", IPv6: true},
	}

	if id, ok := ipv6BundleID(list, "micro_3_2"); !ok || id != "micro_ipv6_3_2" {
		t.Fatalf("micro 的 IPv6 套餐应为 micro_ipv6_3_2，实际 %q ok=%v", id, ok)
	}
	if _, ok := ipv6BundleID(list, "large_3_2"); ok {
		t.Fatal("不存在的规格不应匹配到 IPv6 套餐")
	}
}

// 提交时纠正区域与套餐不匹配（跨代次按基础名映射）
func TestBundleForRegion(t *testing.T) {
	list := []aws.BundleView{
		{ID: "micro_3_1", Base: "micro", TypeName: "micro"},
		{ID: "micro_ipv6_3_1", Base: "micro_ipv6", TypeName: "micro", IPv6: true},
		{ID: "nano_3_1", Base: "nano", TypeName: "nano"},
	}

	if id, ok := bundleForRegion(list, "nano_3_1"); !ok || id != "nano_3_1" {
		t.Fatalf("精确命中失败: %q ok=%v", id, ok)
	}
	if id, ok := bundleForRegion(list, "micro_3_2"); !ok || id != "micro_3_1" {
		t.Fatalf("跨代次应映射到 micro_3_1，实际 %q ok=%v", id, ok)
	}
	if _, ok := bundleForRegion(list, "large_3_2"); ok {
		t.Fatal("区域没有的规格不应映射成功")
	}
}
