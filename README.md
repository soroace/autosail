基于 Go 编写的 AWS Lightsail / EC2 可视化管理工具，支持多账号、多区域实例管理、IP 更换与配额查询等功能。

---

## 一、环境准备

### 1. 安装 Go

请先安装 Go 最新稳定版本（**建议 ≥ 1.25**）。

官方下载地址：<https://go.dev/dl/>

---

#### Windows 安装步骤

1. 打开上方下载地址，选择 `go1.x.x.windows-amd64.msi`（根据最新版本选择）。
2. 双击 `.msi` 安装包，按提示完成安装（默认路径为 `C:\Program Files\Go`）。
3. 安装程序会自动将 Go 的 `bin` 目录添加到系统 `PATH`，**无需手动配置**。
4. 安装完成后，打开新的 **命令提示符（CMD）** 或 **PowerShell**，运行：

```powershell
go version
```

输出类似 `go version go1.x.x windows/amd64` 即表示安装成功。

> 如提示找不到命令，请注销并重新登录，或手动将 `C:\Program Files\Go\bin` 加入系统环境变量 `PATH`。

---

#### Linux 安装步骤（通用 + 可直接复制）

适用发行版：Ubuntu / Debian / CentOS / Rocky / AlmaLinux / Arch 等。

##### 方法 1：官方二进制安装（最推荐）

这是 Go 官方推荐方式，版本更新及时、安装路径清晰、升级可控。

1. 查看最新版本：<https://go.dev/dl/>
2. 下载 Go 压缩包（以下为示例版本，请按最新版本号替换）：

```bash
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
```

3. 删除旧版本（如有）：

```bash
sudo rm -rf /usr/local/go
```

4. 解压到 `/usr/local`：

```bash
sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
```

5. 配置环境变量（推荐同时配置 `PATH` 和 `GOPATH`）：

```bash
# Bash 用户
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export GOPATH=$HOME/go' >> ~/.bashrc
echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
source ~/.bashrc
```

6. 验证安装：

```bash
go version
```

输出类似 `go version go1.25.0 linux/amd64` 表示安装成功。

##### 方法 2：包管理器安装（操作简单，但版本可能偏旧）

```bash
# Ubuntu / Debian
sudo apt update && sudo apt install -y golang-go

# CentOS / RHEL / Rocky / AlmaLinux
sudo yum install -y golang

# Fedora
sudo dnf install -y golang

# Arch Linux
sudo pacman -S --noconfirm go
```

安装后验证：

```bash
go version
```

##### 方法 3：源码安装（仅高级用户需要）

通常不建议日常使用，适用于需要自定义编译参数或研究 Go 源码的场景。

```bash
# 安装基础编译工具（Debian/Ubuntu 示例）
sudo apt update
sudo apt install -y git gcc g++ make

# 获取 Go 源码
git clone https://go.googlesource.com/go ~/go-src
cd ~/go-src/src

# 使用源码脚本构建
./make.bash
```

构建完成后，将生成的二进制加入 PATH（示例路径）：

```bash
echo 'export PATH=$PATH:$HOME/go-src/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

##### Linux 安装最佳实践

- 生产环境优先用“方法 1（官方二进制）”，升级和回滚最稳妥。
- `linux-amd64` 适用于 x86_64；ARM 服务器请下载 `linux-arm64` 包。
- 不要混用多个安装来源（例如同时用 apt 和 /usr/local），避免版本冲突。
- 升级时重复“下载 -> 删除旧版本 -> 解压 -> 验证”即可。

---

## 二、下载项目

```bash
git clone https://github.com/soroice/autosail.git
cd autosail
```

> 如未安装 Git，Windows 可在 <https://git-scm.com/download/win> 下载安装；Linux 执行 `sudo apt install git` 或 `sudo yum install git`。

---

## 三、首次启动与账号注册

项目**无需预先配置账号密码**。

首次访问时，注册页面会自动开放，**第一个注册的账号将自动获得管理员权限**。

1. 启动程序后（见下方第四节），浏览器访问 `http://localhost:9000`。
2. 点击「注册」，填写用户名和密码完成注册（首个用户自动成为管理员）。
3. 使用注册的账号登录即可。

> 管理员可在「系统管理」面板中开启或关闭新用户注册。

---

## 四、运行

### 方式一：直接运行（前台，用于调试）

**Linux / macOS：**

```bash
go run .
```

**Windows（PowerShell / CMD）：**

```powershell
go run .
```

### 方式二：编译后运行（推荐生产使用）

**Linux：**

```bash
go build -o app
./app
```

**Windows（PowerShell）：**

```powershell
go build -o app.exe
.\app.exe
```

程序默认监听 `:9000`，启动后访问 `http://localhost:9000`。

---

## 五、后台运行（Linux）

### 编译并后台启动

```bash
go build -o app
nohup ./app > app.log 2>&1 &
echo "PID: $!"
```

程序将在后台运行，日志输出到 `app.log`。

### 常用管理命令

```bash
# 实时查看日志
tail -f app.log

# 查看进程
ps aux | grep app

# 停止程序
pkill app
```

---

## 六、后台运行（Windows）

### 使用 PowerShell 后台启动

```powershell
Start-Process -FilePath ".\app.exe" -RedirectStandardOutput "app.log" -RedirectStandardError "app_err.log" -WindowStyle Hidden
```

### 查看进程

```powershell
Get-Process app
```

### 停止程序

```powershell
Stop-Process -Name app
```

---

## 七、依赖问题处理

如遇到依赖缺失或版本不一致，可执行：

```bash
go mod tidy
```

---

