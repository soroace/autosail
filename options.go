package main

type RegionOption struct {
	ID   string
	Name string
}

type Option struct {
	ID   string
	Name string
}

var lightsailRegionOptions = []RegionOption{
	{ID: "ap-northeast-1", Name: "东京 Tokyo"},
	{ID: "ap-northeast-2", Name: "首尔 Seoul"},
	{ID: "ap-southeast-1", Name: "新加坡 Singapore"},
	{ID: "ap-southeast-2", Name: "悉尼 Sydney"},
	{ID: "ap-south-1", Name: "孟买 Mumbai"},
	{ID: "us-east-1", Name: "弗吉尼亚 N. Virginia"},
	{ID: "us-east-2", Name: "俄亥俄 Ohio"},
	{ID: "us-west-2", Name: "俄勒冈 Oregon"},
	{ID: "ca-central-1", Name: "加拿大（中部） Canada Central"},
	{ID: "eu-central-1", Name: "法兰克福 Frankfurt"},
	{ID: "eu-west-1", Name: "爱尔兰 Ireland"},
	{ID: "eu-west-2", Name: "伦敦 London"},
	{ID: "eu-west-3", Name: "巴黎 Paris"},
	{ID: "eu-north-1", Name: "斯德哥尔摩 Stockholm"},
}

var ec2RegionOptions = []RegionOption{
	{ID: "af-south-1", Name: "开普敦 Cape Town"},
	{ID: "ap-east-1", Name: "香港 Hong Kong"},
	{ID: "ap-northeast-1", Name: "东京 Tokyo"},
	{ID: "ap-northeast-2", Name: "首尔 Seoul"},
	{ID: "ap-northeast-3", Name: "大阪 Osaka"},
	{ID: "ap-south-1", Name: "孟买 Mumbai"},
	{ID: "ap-south-2", Name: "海得拉巴 Hyderabad"},
	{ID: "ap-southeast-1", Name: "新加坡 Singapore"},
	{ID: "ap-southeast-2", Name: "悉尼 Sydney"},
	{ID: "ap-southeast-3", Name: "雅加达 Jakarta"},
	{ID: "ap-southeast-4", Name: "墨尔本 Melbourne"},
	{ID: "ca-central-1", Name: "加拿大（中部） Canada Central"},
	{ID: "ca-west-1", Name: "加拿大（西部） Canada West"},
	{ID: "eu-central-1", Name: "法兰克福 Frankfurt"},
	{ID: "eu-central-2", Name: "苏黎世 Zurich"},
	{ID: "eu-north-1", Name: "斯德哥尔摩 Stockholm"},
	{ID: "eu-south-1", Name: "米兰 Milan"},
	{ID: "eu-south-2", Name: "西班牙 Spain"},
	{ID: "eu-west-1", Name: "爱尔兰 Ireland"},
	{ID: "eu-west-2", Name: "伦敦 London"},
	{ID: "eu-west-3", Name: "巴黎 Paris"},
	{ID: "il-central-1", Name: "以色列（中部） Israel"},
	{ID: "me-central-1", Name: "阿联酋 UAE"},
	{ID: "me-south-1", Name: "巴林 Bahrain"},
	{ID: "sa-east-1", Name: "圣保罗 São Paulo"},
	{ID: "us-east-1", Name: "弗吉尼亚 N. Virginia"},
	{ID: "us-east-2", Name: "俄亥俄 Ohio"},
	{ID: "us-west-1", Name: "北加州 N. California"},
	{ID: "us-west-2", Name: "俄勒冈 Oregon"},
}

func regionOptionsForService(service string) []RegionOption {
	if service == "ec2" {
		return ec2RegionOptions
	}
	return lightsailRegionOptions
}

func allRegionOptions() []RegionOption {
	all := make([]RegionOption, 0, len(ec2RegionOptions)+len(lightsailRegionOptions))
	all = append(all, ec2RegionOptions...)
	for _, r := range lightsailRegionOptions {
		if !containsRegion(all, r.ID) {
			all = append(all, r)
		}
	}
	return all
}

func containsRegion(list []RegionOption, id string) bool {
	for _, r := range list {
		if r.ID == id {
			return true
		}
	}
	return false
}

var blueprintOptions = []Option{
	{ID: "ubuntu_24_04", Name: "Ubuntu 24.04 LTS"},
	{ID: "ubuntu_22_04", Name: "Ubuntu 22.04 LTS"},
	{ID: "debian_12", Name: "Debian 12"},
	{ID: "debian_11", Name: "Debian 11"},
	{ID: "centos_7", Name: "CentOS 7"},
}

var bundleOptions = []Option{
	{ID: "nano_3_2", Name: "nano (2 vCPUs, 内存 0.5 GB, 硬盘 20 GB, 流量 1024 GB / 月)"},
	{ID: "micro_3_2", Name: "micro (2 vCPUs, 内存 1 GB, 硬盘 40 GB, 流量 2048 GB / 月)"},
	{ID: "small_3_2", Name: "small (2 vCPUs, 内存 2 GB, 硬盘 60 GB, 流量 3072 GB / 月)"},
	{ID: "medium_3_2", Name: "medium (2 vCPUs, 内存 4 GB, 硬盘 80 GB, 流量 4096 GB / 月)"},
	{ID: "large_3_2", Name: "large (2 vCPUs, 内存 8 GB, 硬盘 160 GB, 流量 5120 GB / 月)"},
}

var ipTypeOptions = []Option{
	{ID: "dualstack", Name: "双栈（IPv4+IPv6）"},
	{ID: "ipv6", Name: "仅 IPv6（IPv6-only）"},
	{ID: "ipv4", Name: "仅 IPv4"},
}

var ec2AMIOptions = []Option{
	{ID: "ubuntu-24.04", Name: "Ubuntu 24.04 LTS"},
	{ID: "ubuntu-22.04", Name: "Ubuntu 22.04 LTS"},
	{ID: "debian-12", Name: "Debian 12"},
	{ID: "amzn-2023", Name: "Amazon Linux 2023"},
	{ID: "custom", Name: "自定义 AMI ID"},
}

var ec2InstanceTypes = []Option{
	{ID: "t3.micro", Name: "t3.micro (2 vCPU, 1 GB)"},
	{ID: "t3.small", Name: "t3.small (2 vCPU, 2 GB)"},
	{ID: "t3.medium", Name: "t3.medium (2 vCPU, 4 GB)"},
	{ID: "t3.large", Name: "t3.large (2 vCPU, 8 GB)"},
	{ID: "t3.xlarge", Name: "t3.xlarge (4 vCPU, 16 GB)"},
	{ID: "t3a.micro", Name: "t3a.micro (2 vCPU, 1 GB)"},
	{ID: "t3a.small", Name: "t3a.small (2 vCPU, 2 GB)"},
	{ID: "t3a.medium", Name: "t3a.medium (2 vCPU, 4 GB)"},
	{ID: "t3a.large", Name: "t3a.large (2 vCPU, 8 GB)"},
	{ID: "t4g.micro", Name: "t4g.micro (2 vCPU, 1 GB, ARM)"},
	{ID: "t4g.small", Name: "t4g.small (2 vCPU, 2 GB, ARM)"},
	{ID: "t4g.medium", Name: "t4g.medium (2 vCPU, 4 GB, ARM)"},
	{ID: "t4g.large", Name: "t4g.large (2 vCPU, 8 GB, ARM)"},
	{ID: "m6i.large", Name: "m6i.large (2 vCPU, 8 GB)"},
	{ID: "m6i.xlarge", Name: "m6i.xlarge (4 vCPU, 16 GB)"},
	{ID: "m6g.large", Name: "m6g.large (2 vCPU, 8 GB, ARM)"},
	{ID: "m6g.xlarge", Name: "m6g.xlarge (4 vCPU, 16 GB, ARM)"},
	{ID: "c6i.large", Name: "c6i.large (2 vCPU, 4 GB)"},
	{ID: "c6i.xlarge", Name: "c6i.xlarge (4 vCPU, 8 GB)"},
	{ID: "c6g.large", Name: "c6g.large (2 vCPU, 4 GB, ARM)"},
	{ID: "c6g.xlarge", Name: "c6g.xlarge (4 vCPU, 8 GB, ARM)"},
	{ID: "r6i.large", Name: "r6i.large (2 vCPU, 16 GB)"},
	{ID: "r6i.xlarge", Name: "r6i.xlarge (4 vCPU, 32 GB)"},
	{ID: "r6g.large", Name: "r6g.large (2 vCPU, 16 GB, ARM)"},
	{ID: "r6g.xlarge", Name: "r6g.xlarge (4 vCPU, 32 GB, ARM)"},
	{ID: "custom", Name: "自定义实例类型"},
}

var ipv6BundleMap = map[string]string{
	"nano_3_2":   "nano_ipv6_3_2",
	"micro_3_2":  "micro_ipv6_3_2",
	"small_3_2":  "small_ipv6_3_2",
	"medium_3_2": "medium_ipv6_3_2",
	"large_3_2":  "large_ipv6_3_2",
}
