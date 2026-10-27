package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/aws/smithy-go"
)

// fakeLightsail 只实现测试需要的行为，未用到的接口方法返回零值。
type fakeLightsail struct {
	instances       []types.Instance
	staticIPs       []types.StaticIp
	getStaticIPsErr error
	getStaticIP     func(name string) (*types.StaticIp, error)
	attachErr       error
	releaseErr      error
	calls           []string
}

func (f *fakeLightsail) record(name string) { f.calls = append(f.calls, name) }

func (f *fakeLightsail) called(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (f *fakeLightsail) GetInstances(context.Context, *lightsail.GetInstancesInput, ...func(*lightsail.Options)) (*lightsail.GetInstancesOutput, error) {
	f.record("GetInstances")
	return &lightsail.GetInstancesOutput{Instances: f.instances}, nil
}

func (f *fakeLightsail) GetStaticIps(context.Context, *lightsail.GetStaticIpsInput, ...func(*lightsail.Options)) (*lightsail.GetStaticIpsOutput, error) {
	f.record("GetStaticIps")
	if f.getStaticIPsErr != nil {
		return nil, f.getStaticIPsErr
	}
	return &lightsail.GetStaticIpsOutput{StaticIps: f.staticIPs}, nil
}

func (f *fakeLightsail) GetStaticIp(_ context.Context, in *lightsail.GetStaticIpInput, _ ...func(*lightsail.Options)) (*lightsail.GetStaticIpOutput, error) {
	f.record("GetStaticIp")
	if f.getStaticIP == nil {
		return &lightsail.GetStaticIpOutput{}, nil
	}
	si, err := f.getStaticIP(str(in.StaticIpName))
	if err != nil {
		return nil, err
	}
	return &lightsail.GetStaticIpOutput{StaticIp: si}, nil
}

func (f *fakeLightsail) DetachStaticIp(context.Context, *lightsail.DetachStaticIpInput, ...func(*lightsail.Options)) (*lightsail.DetachStaticIpOutput, error) {
	f.record("DetachStaticIp")
	return &lightsail.DetachStaticIpOutput{}, nil
}

func (f *fakeLightsail) ReleaseStaticIp(context.Context, *lightsail.ReleaseStaticIpInput, ...func(*lightsail.Options)) (*lightsail.ReleaseStaticIpOutput, error) {
	f.record("ReleaseStaticIp")
	if f.releaseErr != nil {
		return nil, f.releaseErr
	}
	return &lightsail.ReleaseStaticIpOutput{}, nil
}

func (f *fakeLightsail) AllocateStaticIp(context.Context, *lightsail.AllocateStaticIpInput, ...func(*lightsail.Options)) (*lightsail.AllocateStaticIpOutput, error) {
	f.record("AllocateStaticIp")
	return &lightsail.AllocateStaticIpOutput{}, nil
}

func (f *fakeLightsail) AttachStaticIp(context.Context, *lightsail.AttachStaticIpInput, ...func(*lightsail.Options)) (*lightsail.AttachStaticIpOutput, error) {
	f.record("AttachStaticIp")
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	return &lightsail.AttachStaticIpOutput{}, nil
}

func (f *fakeLightsail) DeleteInstance(context.Context, *lightsail.DeleteInstanceInput, ...func(*lightsail.Options)) (*lightsail.DeleteInstanceOutput, error) {
	f.record("DeleteInstance")
	return &lightsail.DeleteInstanceOutput{}, nil
}

func (f *fakeLightsail) CreateInstances(context.Context, *lightsail.CreateInstancesInput, ...func(*lightsail.Options)) (*lightsail.CreateInstancesOutput, error) {
	f.record("CreateInstances")
	return &lightsail.CreateInstancesOutput{}, nil
}

func (f *fakeLightsail) OpenInstancePublicPorts(context.Context, *lightsail.OpenInstancePublicPortsInput, ...func(*lightsail.Options)) (*lightsail.OpenInstancePublicPortsOutput, error) {
	f.record("OpenInstancePublicPorts")
	return &lightsail.OpenInstancePublicPortsOutput{}, nil
}

func (f *fakeLightsail) RebootInstance(context.Context, *lightsail.RebootInstanceInput, ...func(*lightsail.Options)) (*lightsail.RebootInstanceOutput, error) {
	f.record("RebootInstance")
	return &lightsail.RebootInstanceOutput{}, nil
}

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

func notFoundErr() error {
	return &smithy.GenericAPIError{Code: "NotFoundException", Message: "not found"}
}

func accessDeniedErr() error {
	return &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"}
}

// 查询静态IP列表失败时必须中止，不能当成「没有静态IP」
func TestSwapStaticIPForInstanceAbortsWhenListFails(t *testing.T) {
	f := &fakeLightsail{
		instances:       []types.Instance{{Name: strPtr("vps1"), PublicIpAddress: strPtr("203.0.113.10")}},
		getStaticIPsErr: accessDeniedErr(),
	}

	err := SwapStaticIPForInstance(context.Background(), f, "vps1")
	if err == nil {
		t.Fatal("查询静态IP列表失败时应中止换IP")
	}
	if !strings.Contains(err.Error(), "权限") {
		t.Fatalf("缺少权限时应给出明确提示，实际: %v", err)
	}
	for _, m := range []string{"DetachStaticIp", "ReleaseStaticIp", "AllocateStaticIp", "AttachStaticIp"} {
		if f.called(m) {
			t.Fatalf("中止后不应调用 %s", m)
		}
	}
}

func TestDeleteInstanceAbortsWhenListFails(t *testing.T) {
	f := &fakeLightsail{getStaticIPsErr: accessDeniedErr()}

	err := DeleteInstanceWithStaticIPCleanup(context.Background(), f, "vps1")
	if err == nil {
		t.Fatal("查询静态IP列表失败时应中止删除")
	}
	if f.called("DeleteInstance") {
		t.Fatal("中止后不应删除实例（否则静态IP会变成计费孤儿）")
	}
}

// 无静态IP的常规路径不应被误判为失败
func TestDeleteInstanceProceedsWhenNoStaticIP(t *testing.T) {
	f := &fakeLightsail{}

	if err := DeleteInstanceWithStaticIPCleanup(context.Background(), f, "vps1"); err != nil {
		t.Fatalf("无静态IP时删除应成功，实际: %v", err)
	}
	if !f.called("DeleteInstance") {
		t.Fatal("应调用 DeleteInstance")
	}
}

// 名称必须唯一：sanitize 会把 web.1/web-1 塌缩成同名
func TestNewStaticIPNameIsUniqueAndValid(t *testing.T) {
	first := newStaticIPName("web.1")
	second := newStaticIPName("web-1")

	if first == second {
		t.Fatalf("不同实例的静态IP名称不应相同: %s", first)
	}
	if !strings.HasPrefix(first, "sip-web1-") {
		t.Fatalf("名称前缀应便于识别实例，实际: %s", first)
	}
	for _, r := range first {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			t.Fatalf("名称含非法字符 %q: %s", r, first)
		}
	}
	// 同一实例连续调用也必须不同（同秒批量/双击场景）
	if a, b := newStaticIPName("vps1"), newStaticIPName("vps1"); a == b {
		t.Fatalf("同一实例连续生成的名称不应相同: %s", a)
	}
}

func TestAPIErrorClassification(t *testing.T) {
	if !isStaticIPNotFound(notFoundErr()) {
		t.Fatal("NotFoundException 应被识别为资源不存在")
	}
	if isStaticIPNotFound(accessDeniedErr()) {
		t.Fatal("权限错误不能被当作资源不存在")
	}
	if !isAccessDenied(accessDeniedErr()) {
		t.Fatal("AccessDeniedException 应被识别为权限错误")
	}
	if isAccessDenied(notFoundErr()) {
		t.Fatal("资源不存在不是权限错误")
	}
	plain := errors.New("network unreachable")
	if isStaticIPNotFound(plain) || isAccessDenied(plain) {
		t.Fatal("普通错误不应被归类")
	}
}

// 回收前必须先看状态：不存在→无需回收；已绑定→不释放；未绑定→才释放
func TestReleaseNewIPChecksStateBeforeReleasing(t *testing.T) {
	t.Run("不存在则无需回收", func(t *testing.T) {
		f := &fakeLightsail{getStaticIP: func(string) (*types.StaticIp, error) { return nil, notFoundErr() }}

		if err := releaseNewIP(context.Background(), f, "sip-x"); err != nil {
			t.Fatalf("IP 不存在不应报错，实际: %v", err)
		}
		if f.called("ReleaseStaticIp") {
			t.Fatal("IP 不存在时不应调用 ReleaseStaticIp")
		}
	})

	t.Run("已绑定则不能释放", func(t *testing.T) {
		f := &fakeLightsail{getStaticIP: func(string) (*types.StaticIp, error) {
			return &types.StaticIp{IsAttached: boolPtr(true)}, nil
		}}

		err := releaseNewIP(context.Background(), f, "sip-x")
		if !errors.Is(err, ErrNewIPAlreadyAttached) {
			t.Fatalf("已绑定的 IP 应返回 ErrNewIPAlreadyAttached，实际: %v", err)
		}
		if f.called("ReleaseStaticIp") {
			t.Fatal("已绑定的 IP 绝不能释放")
		}
	})

	t.Run("未绑定则释放", func(t *testing.T) {
		f := &fakeLightsail{getStaticIP: func(string) (*types.StaticIp, error) {
			return &types.StaticIp{IsAttached: boolPtr(false)}, nil
		}}

		if err := releaseNewIP(context.Background(), f, "sip-x"); err != nil {
			t.Fatalf("未绑定 IP 应释放成功，实际: %v", err)
		}
		if !f.called("ReleaseStaticIp") {
			t.Fatal("未绑定的新 IP 应被释放")
		}
	})
}

// 只有 NotFound 才算已消失，网络错误不能误判成功
func TestWaitStaticIPDetachedDoesNotTreatErrorsAsGone(t *testing.T) {
	f := &fakeLightsail{getStaticIP: func(string) (*types.StaticIp, error) {
		return nil, errors.New("network unreachable")
	}}

	// 超时很短，函数应返回 false（不能因为报错就当作已解绑）
	if WaitStaticIPDetached(context.Background(), f, "sip-old", 10*time.Millisecond) {
		t.Fatal("网络错误不能被当成“已解绑”")
	}
}

// fastRetry 把静态IP重试参数压到最小，避免测试在失败路径上等待
func fastRetry(t *testing.T) {
	t.Helper()
	restore := staticIPRetry
	staticIPRetry.Alloc, staticIPRetry.Attach, staticIPRetry.Release, staticIPRetry.Sleep = 1, 1, 1, 0
	t.Cleanup(func() { staticIPRetry = restore })
}

// 绑定请求返回失败、但 IP 实际已绑定：必须保留哨兵身份供上层 errors.Is 判断，且不能释放该 IP
func TestSwapStaticIPKeepsSentinelWhenActuallyAttached(t *testing.T) {
	fastRetry(t)
	f := &fakeLightsail{
		instances: []types.Instance{{Name: strPtr("vps1"), PublicIpAddress: strPtr("203.0.113.10")}},
		attachErr: errors.New("connection reset by peer"),
		getStaticIP: func(string) (*types.StaticIp, error) {
			return &types.StaticIp{IsAttached: boolPtr(true)}, nil
		},
	}

	err := SwapStaticIPForInstance(context.Background(), f, "vps1")
	if err == nil {
		t.Fatal("绑定失败时应返回错误")
	}
	if !errors.Is(err, ErrNewIPAlreadyAttached) {
		t.Fatalf("应保留哨兵身份，实际: %v", err)
	}
	if f.called("ReleaseStaticIp") {
		t.Fatal("实际已绑定的 IP 不能被释放")
	}
}

// 释放旧 IP 时返回 NotFound（上一次实际成功、响应丢失）：应视为释放成功而非失败
func TestReleaseNotFoundTreatedAsSuccess(t *testing.T) {
	f := &fakeLightsail{
		staticIPs:  []types.StaticIp{{Name: strPtr("sip-old"), AttachedTo: strPtr("vps1"), IsAttached: boolPtr(true)}},
		releaseErr: notFoundErr(),
		getStaticIP: func(string) (*types.StaticIp, error) {
			return nil, notFoundErr()
		},
	}

	name, err := DeletePreviousStaticIPOnlyForInstance(context.Background(), f, "vps1")
	if err != nil {
		t.Fatalf("NotFound 应视为释放成功，实际报错: %v", err)
	}
	if name != "sip-old" {
		t.Fatalf("应返回已释放的 IP 名，实际: %q", name)
	}
}

// 绑定失败且 IP 未绑定：应回收该 IP，并返回普通失败
func TestSwapStaticIPReleasesWhenAttachFailed(t *testing.T) {
	fastRetry(t)
	f := &fakeLightsail{
		instances: []types.Instance{{Name: strPtr("vps1"), PublicIpAddress: strPtr("203.0.113.10")}},
		attachErr: errors.New("connection reset by peer"),
		getStaticIP: func(string) (*types.StaticIp, error) {
			return &types.StaticIp{IsAttached: boolPtr(false)}, nil
		},
	}

	err := SwapStaticIPForInstance(context.Background(), f, "vps1")
	if err == nil {
		t.Fatal("绑定失败时应返回错误")
	}
	if errors.Is(err, ErrNewIPAlreadyAttached) {
		t.Fatal("IP 未绑定时不应返回哨兵错误")
	}
	if !f.called("ReleaseStaticIp") {
		t.Fatal("未绑定的新 IP 应被回收")
	}
}
