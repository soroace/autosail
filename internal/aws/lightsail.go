package aws

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/aws/smithy-go"
)

type InstanceView struct {
	Name       string
	State      string
	PublicIPv4 string
	PublicIPv6 string
	StaticIPv4 string
	Zone       string
	BundleID   string
	Created    string
}

func ListInstances(ctx context.Context, cli LightsailAPI) ([]InstanceView, error) {
	out, err := cli.GetInstances(ctx, &lightsail.GetInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("拉取实例失败：%v", err)
	}

	// static ips
	sipOut, _ := cli.GetStaticIps(ctx, &lightsail.GetStaticIpsInput{})
	staticMap := map[string]string{} // instanceName -> staticIPv4
	if sipOut != nil {
		for _, si := range sipOut.StaticIps {
			if si.AttachedTo != nil && si.IpAddress != nil {
				// be stricter: ensure it is actually attached
				if si.IsAttached == nil || *si.IsAttached {
					staticMap[*si.AttachedTo] = *si.IpAddress
				}
			}
		}
	}

	var list []InstanceView
	for _, ins := range out.Instances {
		name := str(ins.Name)
		state := ""
		if ins.State != nil && ins.State.Name != nil {
			state = *ins.State.Name
		}
		public4 := str(ins.PublicIpAddress)
		public6 := ""
		if len(ins.Ipv6Addresses) > 0 {
			public6 = ins.Ipv6Addresses[0]
		}

		zone := str(ins.Location.AvailabilityZone)
		created := ""
		if ins.CreatedAt != nil {
			created = ins.CreatedAt.Format("2006-01-02 15:04:05")
		}

		list = append(list, InstanceView{
			Name:       name,
			State:      state,
			PublicIPv4: public4,
			PublicIPv6: public6,
			StaticIPv4: staticMap[name],
			Zone:       zone,
			BundleID:   str(ins.BundleId),
			Created:    created,
		})
	}
	// 按名称字母排序
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list, nil
}

type CreateInstanceInput struct {
	InstanceName     string
	AvailabilityZone string
	BlueprintID      string
	BundleID         string
	UserData         string
	IPAddressType    string // dualstack/ipv6
	EnableFWAll      bool
}

func CreateInstance(ctx context.Context, cli LightsailAPI, in CreateInstanceInput) error {
	ipType := in.IPAddressType
	if ipType == "" {
		ipType = "dualstack"
	}
	_, err := cli.CreateInstances(ctx, &lightsail.CreateInstancesInput{
		InstanceNames:    []string{in.InstanceName},
		AvailabilityZone: &in.AvailabilityZone,
		BlueprintId:      &in.BlueprintID,
		BundleId:         &in.BundleID,
		UserData:         &in.UserData,
		IpAddressType:    types.IpAddressType(ipType),
	})
	if err != nil {
		return fmt.Errorf("创建实例失败：%v", err)
	}

	if in.EnableFWAll {
		// 等待实例进入 running 状态后再开放全端口（实例刚创建时开端口易失败）
		if err := waitInstanceRunning(ctx, cli, in.InstanceName, 150*time.Second); err != nil {
			return fmt.Errorf("实例已创建，但等待就绪超时：%v", err)
		}
		var portErr error
		for attempt := 0; attempt < 3; attempt++ {
			_, portErr = cli.OpenInstancePublicPorts(ctx, &lightsail.OpenInstancePublicPortsInput{
				InstanceName: &in.InstanceName,
				PortInfo: &types.PortInfo{
					FromPort: 0,
					ToPort:   65535,
					Protocol: types.NetworkProtocolAll,
				},
			})
			if portErr == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if portErr != nil {
			// 实例已创建，仅开端口失败，保留可见性
			return fmt.Errorf("已创建，但开启全端口失败：%v", portErr)
		}
	}
	return nil
}

// waitInstanceRunning 轮询等待 Lightsail 实例进入 running 状态
func waitInstanceRunning(ctx context.Context, cli LightsailAPI, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := cli.GetInstances(ctx, &lightsail.GetInstancesInput{})
		if err == nil && out != nil {
			for _, ins := range out.Instances {
				if str(ins.Name) == name {
					if ins.State != nil && ins.State.Name != nil && *ins.State.Name == "running" {
						return nil
					}
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("实例 %s 未在 %v 内进入 running 状态", name, timeout)
}

func RebootInstance(ctx context.Context, cli LightsailAPI, name string) error {
	return SafeRetry("重启实例", 6, 1200*time.Millisecond, func() error {
		_, err := cli.RebootInstance(ctx, &lightsail.RebootInstanceInput{InstanceName: &name})
		return err
	})
}

func OpenAllPorts(ctx context.Context, cli LightsailAPI, instanceName string) error {
	return SafeRetry("开放全端口", 6, 1200*time.Millisecond, func() error {
		_, err := cli.OpenInstancePublicPorts(ctx, &lightsail.OpenInstancePublicPortsInput{
			InstanceName: &instanceName,
			PortInfo: &types.PortInfo{
				FromPort: 0,
				ToPort:   65535,
				Protocol: types.NetworkProtocolAll,
			},
		})
		return err
	})
}

func DeleteInstanceWithStaticIPCleanup(ctx context.Context, cli LightsailAPI, name string) error {
	// 清理失败则中止，避免留下计费孤儿 IP
	if _, err := DeletePreviousStaticIPOnlyForInstance(ctx, cli, name); err != nil {
		return fmt.Errorf("清理静态IP失败，已中止删除：%v", err)
	}

	return SafeRetry("删除实例", 8, 1200*time.Millisecond, func() error {
		_, err := cli.DeleteInstance(ctx, &lightsail.DeleteInstanceInput{InstanceName: &name})
		return err
	})
}

// errNewIPAlreadyAttached：新 IP 实际已绑定成功（响应丢失），此时绝不能释放
var errNewIPAlreadyAttached = errors.New("新静态IP已处于绑定状态")

// isAPIErrorCode 按 AWS 错误码区分「资源不存在」与网络/权限等错误
func isAPIErrorCode(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, code := range codes {
		if apiErr.ErrorCode() == code {
			return true
		}
	}
	return false
}

func isStaticIPNotFound(err error) bool {
	return isAPIErrorCode(err, "NotFoundException", "ResourceNotFoundException", "NoSuchEntity")
}

func isAccessDenied(err error) bool {
	return isAPIErrorCode(err, "AccessDeniedException", "UnauthorizedException", "AccessDenied")
}

// newStaticIPName 生成唯一名称，避免撞名时误释放他人的同名 IP
func newStaticIPName(instanceName string) string {
	base := sanitize(instanceName)
	if len(base) > 40 {
		base = base[:40]
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("sip-%s-%d-%s", base, time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// releaseNewIP 回收未成功绑定的新 IP；已绑定则返回 errNewIPAlreadyAttached。用 WithoutCancel 保证关页面后仍能回收
func releaseNewIP(ctx context.Context, cli LightsailAPI, name string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	out, err := cli.GetStaticIp(cleanupCtx, &lightsail.GetStaticIpInput{StaticIpName: &name})
	if err != nil {
		if isStaticIPNotFound(err) {
			return nil
		}
		return fmt.Errorf("确认 %s 状态失败：%v", name, err)
	}
	if out == nil || out.StaticIp == nil {
		return nil
	}
	if out.StaticIp.IsAttached != nil && *out.StaticIp.IsAttached {
		return errNewIPAlreadyAttached
	}
	return SafeRetry("释放未绑定的新静态IP", 4, 1200*time.Millisecond, func() error {
		_, err := cli.ReleaseStaticIp(cleanupCtx, &lightsail.ReleaseStaticIpInput{StaticIpName: &name})
		return err
	})
}

func SwapStaticIPForInstance(ctx context.Context, cli LightsailAPI, instanceName string) error {
	// sanity: ipv6-only instances cannot use IPv4 Static IP
	insOut, err := cli.GetInstances(ctx, &lightsail.GetInstancesInput{})
	if err == nil && insOut != nil {
		for _, ins := range insOut.Instances {
			if str(ins.Name) == instanceName {
				if str(ins.PublicIpAddress) == "" {
					return fmt.Errorf("该实例无公网 IPv4（可能是 IPv6-only），无法换静态IP")
				}
				break
			}
		}
	}

	// 旧 IP 未释放成功则中止，避免新旧两份计费
	if _, err := DeletePreviousStaticIPOnlyForInstance(ctx, cli, instanceName); err != nil {
		return fmt.Errorf("清理旧静态IP失败，已中止换IP：%v", err)
	}

	// allocate new and attach
	newName := newStaticIPName(instanceName)

	if err := SafeRetry("申请新静态IP", 8, 1200*time.Millisecond, func() error {
		_, err := cli.AllocateStaticIp(ctx, &lightsail.AllocateStaticIpInput{StaticIpName: &newName})
		return err
	}); err != nil {
		// 重试途中可能已创建成功，先查状态再决定是否回收
		if cleanupErr := releaseNewIP(ctx, cli, newName); cleanupErr != nil {
			return fmt.Errorf("申请静态IP失败：%v；回收未确认：%v", err, cleanupErr)
		}
		return fmt.Errorf("申请静态IP失败：%v", err)
	}

	if err := SafeRetry("绑定新静态IP", 8, 1200*time.Millisecond, func() error {
		_, err := cli.AttachStaticIp(ctx, &lightsail.AttachStaticIpInput{
			StaticIpName: &newName,
			InstanceName: &instanceName,
		})
		return err
	}); err != nil {
		cleanupErr := releaseNewIP(ctx, cli, newName)
		switch {
		case errors.Is(cleanupErr, errNewIPAlreadyAttached):
			return fmt.Errorf("换 IP 可能已成功，请刷新确认：%v", err)
		case cleanupErr != nil:
			return fmt.Errorf("绑定静态IP失败：%v（%s 未释放，请手动处理）", err, newName)
		default:
			return fmt.Errorf("绑定静态IP失败（新 IP 已回收）：%v", err)
		}
	}

	return nil
}

func DeletePreviousStaticIPOnlyForInstance(ctx context.Context, cli LightsailAPI, instanceName string) (string, error) {
	oldName, _, err := FindAttachedStaticIPName(ctx, cli, instanceName)
	if err != nil {
		// 查询失败不能当成「没有静态IP」
		return "", err
	}
	if oldName == "" {
		return "", nil
	}

	if err := SafeRetry("解绑旧静态IP", 8, 1200*time.Millisecond, func() error {
		_, err := cli.DetachStaticIp(ctx, &lightsail.DetachStaticIpInput{StaticIpName: &oldName})
		return err
	}); err != nil {
		return "", err
	}

	ok := WaitStaticIPDetached(ctx, cli, oldName, 120*time.Second)
	if !ok {
		return "", fmt.Errorf("旧静态IP解绑超时：%s", oldName)
	}

	if err := SafeRetry("释放旧静态IP", 12, 1300*time.Millisecond, func() error {
		_, err := cli.ReleaseStaticIp(ctx, &lightsail.ReleaseStaticIpInput{StaticIpName: &oldName})
		return err
	}); err != nil {
		return "", err
	}

	// 只有 NotFound 才算释放成功，网络/限流错误不能误判为已消失
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := cli.GetStaticIp(ctx, &lightsail.GetStaticIpInput{StaticIpName: &oldName})
		if err != nil {
			if isStaticIPNotFound(err) {
				return oldName, nil
			}
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	if lastErr != nil {
		return "", fmt.Errorf("确认静态IP释放失败：%v", lastErr)
	}
	return "", fmt.Errorf("旧静态IP仍存在（释放未生效）：%s", oldName)
}

// FindAttachedStaticIPName 查询失败必须返回 error，否则会被误判为「没有静态IP」
func FindAttachedStaticIPName(ctx context.Context, cli LightsailAPI, instanceName string) (string, string, error) {
	out, err := cli.GetStaticIps(ctx, &lightsail.GetStaticIpsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return "", "", fmt.Errorf("缺少 lightsail:GetStaticIps 权限：%v", err)
		}
		return "", "", fmt.Errorf("查询静态IP列表失败：%v", err)
	}
	if out == nil {
		return "", "", nil
	}
	for _, si := range out.StaticIps {
		if si.AttachedTo != nil && *si.AttachedTo == instanceName {
			if si.IsAttached == nil || *si.IsAttached {
				return str(si.Name), str(si.IpAddress), nil
			}
		}
	}
	return "", "", nil
}

func WaitStaticIPDetached(ctx context.Context, cli LightsailAPI, staticIPName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := cli.GetStaticIp(ctx, &lightsail.GetStaticIpInput{StaticIpName: &staticIPName})
		if err != nil {
			if isStaticIPNotFound(err) {
				return true // 已不存在
			}
			// 其它错误不能当成已解绑，继续等待
			time.Sleep(2 * time.Second)
			continue
		}
		if out == nil || out.StaticIp == nil {
			return true
		}
		if out.StaticIp.IsAttached != nil && !*out.StaticIp.IsAttached {
			return true
		}
		if out.StaticIp.AttachedTo == nil || *out.StaticIp.AttachedTo == "" {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func BuildRootPasswordUserData(password string) string {
	// 按你 python 逻辑（核心部分）：设置 root 密码、修改 sshd_config、重启 ssh
	// 注意：保持脚本尽量兼容常见发行版
	// 使用 base64 传递密码，避免 $, `, \, 换行等被 shell 解释；大多数发行版自带 coreutils/busybox 的 base64。
	pw := base64.StdEncoding.EncodeToString([]byte(password))
	return fmt.Sprintf(`#!/bin/bash
set -e

if [[ $(id -u) != 0 ]]; then
  echo -e "\033[31m 必须以root方式运行脚本 \033[0m"
  exit 1
fi

password_b64="%s"
password="$(printf '%%s' "$password_b64" | base64 -d)"

echo "root:$password" | chpasswd
passwd -u root || true

sed -i 's@^\(Include[ ]*/etc/ssh/sshd_config.d/\*\.conf\)@# \1@' /etc/ssh/sshd_config
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/g;s/^#\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config
sed -i 's/^#\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config
sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config
sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config

# 重启 SSH（兼容）
if [ -f /etc/os-release ]; then
  if [ "$(awk -F= '/VERSION_CODENAME/{print $2}' /etc/os-release)" = 'noble' ]; then
    systemctl restart ssh || true
  elif [[ "$(grep 'PRETTY_NAME' /etc/os-release)" =~ 'Alpine' ]]; then
    service sshd restart || true
  else
    systemctl restart sshd || true
  fi
else
  systemctl restart ssh >/dev/null 2>&1 || true
  systemctl restart sshd >/dev/null 2>&1 || true
  service sshd restart >/dev/null 2>&1 || true
fi

echo -e "\033[32m 请重新登录，用户名：root ， 密码：$password \033[0m"
`, pw)
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "x"
	}
	return out
}

func str[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
