# 容器部署（Docker / Podman）

镜像同时提供 `linux/amd64` 与 `linux/arm64`（NAS 上的 ARM 机型也能跑）。

## 一条命令跑起来

```bash
docker run -d --name planeduel \
  -p 28082:28082 \
  -p 127.0.0.1:18088:18088 \
  -e PD_PUBLIC_PORT_ENABLED=1 \
  -v planeduel-data:/data \
  --restart unless-stopped \
  laoknas/planeduel:latest
```

打开 `http://<宿主机IP>:28082/` 即可开局；管理入口在 `http://127.0.0.1:18088/`。

> **务必**把 18088 映射成 `127.0.0.1:18088`。管理权限按入口判定，本机回环即管理员，
> 直接暴露 `18088:18088` 等于把这个能力开放给整个局域网。

## docker compose

```bash
cd deploy
cp compose.yaml /your/path/compose.yaml   # 或直接在该目录执行
docker compose up -d
```

可用 `.env` 覆盖：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PLANEDUEL_VERSION` | `latest` | 镜像 tag |
| `PLANEDUEL_PORT` | `28082` | 对外端口 |
| `PLANEDUEL_BIND` | `0.0.0.0` | 绑定地址（只想局域网用可改 `192.168.x.x`） |
| `PUID` / `PGID` | `1000` | 运行用户，需对挂载目录有写权限 |
| `TZ` | `Asia/Shanghai` | 时区 |

## 环境变量（容器里常用）

| 变量 | 示例 | 说明 |
| --- | --- | --- |
| `PD_PUBLIC_PORT` | `28082` | 对外端口号 |
| `PD_PUBLIC_PORT_ENABLED` | `1` | 打开对外端口（配置文件默认是关的） |
| `PD_NICK_TTL_DAYS` | `30` | 游客昵称兜底保留天数 |
| `PD_DATA` | `/data` | 数据目录 |
| `ADMIN_LISTEN` | `0.0.0.0:18088` | 管理口监听地址（仅容器内，宿主机要绑回环） |

环境变量优先级高于 `config.json`，**每次启动都会生效**；想让面板里的修改长期有效，就不要重复设置同名环境变量。

## 数据与备份

容器只持久化一个卷：`/data`（内含 `config.json` 与运行日志）。备份：

```bash
docker run --rm -v planeduel-data:/data -v "$PWD":/backup alpine \
  tar -czf /backup/planeduel-data.tgz -C /data .
```

## 和飞牛版本的区别

| 能力 | 飞牛 fnOS（fpk） | 容器 |
| --- | --- | --- |
| 统一网关入口 | ✅ 单点登录，页面固定「管理员」 | ❌ 无网关，走端口 |
| 独立端口 | 面板开关控制，默认关 | 默认开（`PD_PUBLIC_PORT_ENABLED=1`） |
| 管理入口 | `127.0.0.1:18088` | 同样存在，但要自己映射且绑回环 |
| 数据目录 | `/vol1/@appdata/planeduel` | 卷 `/data` |
