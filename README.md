# 纸上打飞机 · 联机版

小时候课间在纸上玩的那个游戏 —— 10×10 方格，每方三架 1513 形飞机，轮流喊坐标，打中机头才算击落。

现在是**飞牛 fnOS 上的一个应用**，也支持容器和单文件二进制运行。Go 单二进制后端 + 原生前端，零第三方依赖。

![version](https://img.shields.io/badge/version-0.9.8-blue)
![go](https://img.shields.io/badge/Go-1.27-00ADD8)
![license](https://img.shields.io/badge/license-MIT-green)
![platform](https://img.shields.io/badge/platform-amd64%20%7C%20arm64-lightgrey)
![ghcr](https://img.shields.io/badge/ghcr.io-planeduel-2496ED?logo=docker&logoColor=white)

## 特性

- **规则**：10×10 棋盘，每方三架飞机（各 10 格），机头命中即击落，三架全灭判胜
- **可选规则**：交替出手 / 打中连击、死机残格「骗人」、每回合 3 或 5 分钟
- **对局**：邀请、房间码加入、满员自动旁观、结束后再来一局
- **容错**：断线 2 分钟内重连接管座位，超时判负；回合超时会明确说明原因
- **入口**：飞牛统一网关（免登录）、独立端口（游客自动昵称）、本机回环管理口
- **界面**：PC / 平板 / 手机自适应，纸质棋盘风格，无需图片资源

## 玩法

1. 每方在 10×10 格上摆三架「1513 形」飞机（形状类似 `..X..` / `XXXXX` / `..X..` / `.XXX.` 的位置组合，可旋转）。
2. 双方轮流喊坐标（行 A–J，列 1–10）。
3. 对方回答：**空**（没打到）、**中**（打到机身/机翼/机尾）、**死**（打到机头，该架报废）。
4. 先把对方三架飞机的机头全部打中者获胜。

## 安装

### 飞牛 fnOS

从 [Releases](https://github.com/kuai410022283/planeduel/releases/latest) 下载 `planeduel_all_v*.fpk`，
在飞牛「App Center → 手动安装」里选择该文件。安装后从桌面图标进入，昵称固定为「管理员」。

安装包同时包含 `x86_64` 与 `aarch64` 程序，会按机型自动选用。

### 容器

官方镜像发布在 GitHub Container Registry，**公开可匿名拉取**，支持 `linux/amd64` 与 `linux/arm64`：

```bash
docker pull ghcr.io/kuai410022283/planeduel:latest
```

| 标签 | 含义 |
| --- | --- |
| `latest` | 最新构建 |
| `0.9.8` | 指定版本（每次发版固定一个） |
| `0.9` | 同一小版本的最新修订 |

启动：

```bash
docker run -d --name planeduel \
  -p 28082:28082 \
  -p 127.0.0.1:18088:18088 \
  -e PD_PUBLIC_PORT_ENABLED=1 \
  -v planeduel-data:/data \
  --restart unless-stopped \
  ghcr.io/kuai410022283/planeduel:latest
```

打开 `http://<宿主机IP>:28082/` 即可开局；管理入口是 `http://127.0.0.1:18088/`。

`docker compose` 方式：

```yaml
services:
  planeduel:
    image: ghcr.io/kuai410022283/planeduel:latest
    restart: unless-stopped
    ports:
      - "28082:28082"
      - "127.0.0.1:18088:18088"
    environment:
      PD_PUBLIC_PORT_ENABLED: "1"
      TZ: Asia/Shanghai
    volumes:
      - planeduel-data:/data

volumes:
  planeduel-data:
```

> 管理口 18088 只映射到 `127.0.0.1`：管理权限按入口判定，本机回环即管理员，不要暴露到局域网。

也可以从源码自建：`docker build -t planeduel .`（或 `make docker`）。更多参数与备份说明见 [deploy/docker.md](deploy/docker.md)。

### 单文件二进制

Releases 里按平台下载，解压后运行：

```bash
# Linux / macOS
PD_PUBLIC_PORT_ENABLED=1 ./planeduel -data ./data

# Windows
set PD_PUBLIC_PORT_ENABLED=1 && planeduel.exe -data data
```

支持 `linux/amd64`、`linux/arm64`、`linux/armv7`、`windows/amd64`、`windows/arm64`、`darwin/amd64`、`darwin/arm64`。

## 入口与权限

| 入口 | 身份 | 管理权限 | 说明 |
| --- | --- | --- | --- |
| 飞牛网关 `/app/planeduel/` | 固定「管理员」 | 有 | 免登录，随飞牛桌面图标打开 |
| 独立端口 `:28082` | 游客，自动分配「打飞机 + 随机后缀」 | 无 | 可自行改名，昵称全局唯一 |
| 本机回环 `127.0.0.1:18088` | 本机 | 有 | 端口关闭或网关异常时的恢复入口 |

昵称规则：离线超过 10 分钟自动释放；正在对局或观战中的昵称不会被回收。

## 配置

数据目录下的 `config.json`：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `public_port_enabled` | `false` | 是否开启独立端口 |
| `public_port` | `28082` | 端口号 |
| `nick_ttl_days` | `30` | 游客昵称兜底保留天数 |

端口被其它程序占用时，应用会自动在 `28000–28099` 里往后找一个可用端口（最多试 20 个），
把新端口写回配置并在设置面板提示「端口 28082 已被占用，已自动改用 28083」。
如果整段都被占满，则自动关闭对外访问并说明原因，不会留下「显示已开启、实际没监听」的状态。

环境变量（优先级高于配置文件，容器部署常用）：

| 变量 | 说明 |
| --- | --- |
| `PD_DATA` | 数据目录（默认 `./data`） |
| `PD_PUBLIC_PORT` | 对外端口号 |
| `PD_PUBLIC_PORT_ENABLED` | 是否开启对外端口（`1`/`true`/`yes`/`on`） |
| `PD_NICK_TTL_DAYS` | 游客昵称兜底 TTL |
| `PD_ADMIN_LISTEN` | 管理口监听地址（默认 `127.0.0.1:8081`） |

## 构建

```bash
make test     # 全部单测
make build    # 交叉编译 x86_64 + aarch64
make pack     # 打包 fnOS 安装包（首次自动下载 fnpack）
make docker   # 构建容器镜像
make all      # test + pack
```

推送 tag `v*` 会由 GitHub Actions 自动产出容器镜像、7 个平台压缩包与 fnOS 安装包。

## 目录结构

```
cmd/server/         服务端入口
internal/game/      规则引擎（形状、棋盘、对局、回放）
internal/app/       房间、会话、WebSocket、端口管理
internal/app/web/   前端（原生 HTML/CSS/JS，随二进制内嵌）
dist/pkg/           fnOS 打包源（manifest、生命周期脚本、图标）
deploy/             容器 compose / entrypoint / 部署说明
```

## 许可

[MIT](LICENSE)
