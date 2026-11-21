#!/bin/sh
# 容器入口：修正 /data 归属 → 检查可写 → 降权到 PUID:PGID 后启动
set -eu

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"
DATA_DIR="${DATA_DIR:-/data}"
# 容器内管理口必须监听 0.0.0.0 才能被 -p 映射出来；
# 宿主机侧请务必写成 127.0.0.1:18088:18088，不要暴露到公网。
ADMIN_LISTEN="${ADMIN_LISTEN:-0.0.0.0:18088}"

if ! chown -R "$PUID:$PGID" "$DATA_DIR" 2>/dev/null; then
  echo "planeduel: warning: 无法修改 $DATA_DIR 的全部归属（NFS/CIFS 属正常），接下来用写测试判断" >&2
fi

if ! su-exec "$PUID:$PGID" sh -c "mkdir -p '$DATA_DIR' && touch '$DATA_DIR/.write-check' && rm -f '$DATA_DIR/.write-check'"; then
  cat >&2 <<EOF
planeduel: '$DATA_DIR' 对 ${PUID}:${PGID} 不可写。
  - 把宿主机挂载目录的属主改成 ${PUID}:${PGID}，或设置 PUID/PGID 为该目录的属主；
  - NFS root_squash / CIFS / 只读挂载无法在容器内改归属，请用正确的 uid/gid 挂载。
EOF
  exit 1
fi

echo "planeduel: 启动（data=${DATA_DIR} admin=${ADMIN_LISTEN} port=${PD_PUBLIC_PORT:-28082} enabled=${PD_PUBLIC_PORT_ENABLED:-0}）"
exec su-exec "$PUID:$PGID" /app/planeduel \
  -data "$DATA_DIR" \
  -admin-listen "$ADMIN_LISTEN" \
  "$@"
