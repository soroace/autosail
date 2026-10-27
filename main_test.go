package main

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"testing"
	"time"

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
		"instPollMS":  instPollIntervalMS,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatalf("解析模板失败: %v", err)
	}
	return tmpl
}

// 实例列表片段：两个服务各自的徽章与卡片应正常渲染（列表改为异步渲染）
func TestTemplatesRenderInstances(t *testing.T) {
	tmpl := parseTemplates(t)

	cases := []struct {
		name string
		data InstanceSectionData
	}{
		{
			name: "lightsail",
			data: InstanceSectionData{
				CSRFToken: "tok", Region: "ap-northeast-1", ManageService: "lightsail",
				Instances: []aws.InstanceView{{Name: "vps-running", State: "running"}, {Name: "vps-pending", State: "pending"}},
			},
		},
		{
			name: "ec2",
			data: InstanceSectionData{
				CSRFToken: "tok", Region: "ap-northeast-1", ManageService: "ec2",
				EC2Instances: []aws.EC2InstanceView{{ID: "i-1", State: "running"}, {ID: "i-2", State: "pending"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "instanceSection", tc.data); err != nil {
				t.Fatalf("渲染 instanceSection 失败: %v", err)
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

// 拉取失败（无凭据/AWS 报错）时片段内应显示错误，而不是渲染成空列表
func TestInstanceSectionShowsError(t *testing.T) {
	tmpl := parseTemplates(t)

	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "instanceSection", InstanceSectionData{Error: "拉取实例失败：超时"})
	if err != nil {
		t.Fatalf("渲染 instanceSection 失败: %v", err)
	}
	if !strings.Contains(buf.String(), "拉取实例失败") {
		t.Fatal("片段应显示错误信息")
	}
}

// 管理页外壳应只输出异步容器（不阻塞 AWS），并带上前端需要的 data 属性
func TestManagePageRendersAsyncContainer(t *testing.T) {
	tmpl := parseTemplates(t)

	data := PageData{
		Title: "AutoSail", Username: "tester", Tab: "manage",
		HasCreds: true, ManageService: "lightsail", Region: "ap-northeast-1",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		t.Fatalf("渲染 layout 失败: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`id="instanceList"`,
		`data-service="lightsail"`,
		`data-region="ap-northeast-1"`,
		"加载中",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("管理页缺少 %q", want)
		}
	}
}

// 轮询间隔必须由服务端按缓存时长注入，避免前端另写一份硬编码
func TestTemplatesInjectPollInterval(t *testing.T) {
	tmpl := parseTemplates(t)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", PageData{Title: "AutoSail", Username: "tester"}); err != nil {
		t.Fatalf("渲染 layout 失败: %v", err)
	}

	want := fmt.Sprintf(`data-inst-poll-ms="%d"`, instPollIntervalMS())
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("渲染结果缺少 %q（前端轮询间隔未注入）", want)
	}
	if instPollIntervalMS() <= int(instCacheTTL/time.Millisecond) {
		t.Fatalf("轮询间隔必须大于缓存时长：poll=%dms ttl=%dms", instPollIntervalMS(), instCacheTTL/time.Millisecond)
	}
}

func TestLoginTemplateRenders(t *testing.T) {
	tmpl := parseTemplates(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "login", PageData{Title: "AutoSail"}); err != nil {
		t.Fatalf("渲染 login 失败: %v", err)
	}
}
