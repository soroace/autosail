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
