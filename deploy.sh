#!/bin/sh
# Run beside docker-compose.yml and your existing .env; never rebuild on server.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$script_dir"

cleanup_images() {
    removed=0
    candidates=$(docker image ls --filter dangling=true --quiet --no-trunc)
    for image_id in $candidates; do
        # Support both labelled releases and older MeldMail images.
        identity=$(docker image inspect "$image_id" --format '{{index .Config.Labels "io.meldmail.project"}}|{{json .Config.Entrypoint}}|{{.Config.WorkingDir}}')
        case "$identity" in
            'meldmail|'*|*'|["/app/meldmail"]|/app') ;;
            *) continue ;;
        esac
        referenced=$(docker ps --all --quiet --filter "ancestor=$image_id")
        if [ -n "$referenced" ]; then
            continue
        fi
        if docker image rm "$image_id"; then
            removed=$((removed + 1))
        else
            printf '未删除镜像 %s（可能刚被其他容器引用）。\n' "$image_id" >&2
        fi
    done
    printf '已清理 %s 个 MeldMail 悬空镜像。\n' "$removed"
}

case "${1:-}" in
    --help|-h)
        printf '用法：%s [--cleanup-only]\n默认拉取镜像、启动并检查服务，然后清理本项目悬空镜像。\n' "$0"
        exit 0
        ;;
    ''|--cleanup-only) ;;
    *) printf '未知参数：%s\n' "$1" >&2; exit 2 ;;
esac
if [ "$#" -gt 1 ]; then
    printf '只接受一个可选参数 --cleanup-only。\n' >&2
    exit 2
fi
command -v docker >/dev/null 2>&1 || { printf '请先安装 Docker。\n' >&2; exit 1; }
docker info >/dev/null
if [ "${1:-}" = '--cleanup-only' ]; then
    cleanup_images
    exit 0
fi

docker compose version >/dev/null
[ -f docker-compose.yml ] || { printf '缺少 docker-compose.yml，请将脚本放在项目目录中。\n' >&2; exit 1; }
[ -f .env ] || { printf '请先复制 .env.example 为 .env，并填写主密钥和管理员密码。\n' >&2; exit 1; }
docker compose --env-file .env config --quiet
# Ask Compose to parse .env (never source it as shell code).
compose_environment=$(docker compose --env-file .env config --environment)
host_proxy_port=$(printf '%s\n' "$compose_environment" | sed -n 's/^MELDMAIL_HOST_PROXY_PORT=//p')
unset compose_environment
case "$host_proxy_port" in
    '') ;;
    *[!0-9]*) printf 'MELDMAIL_HOST_PROXY_PORT 必须是 1–65535 的端口号。\n' >&2; exit 2 ;;
    *) if [ "${#host_proxy_port}" -gt 5 ] || [ "$host_proxy_port" -lt 1 ] || [ "$host_proxy_port" -gt 65535 ]; then
        printf 'MELDMAIL_HOST_PROXY_PORT 必须是 1–65535 的端口号。\n' >&2; exit 2
       fi ;;
esac
compose() {
    if [ -n "$host_proxy_port" ]; then
        if [ -f docker-compose.override.yml ]; then
            docker compose --env-file .env -f docker-compose.yml -f docker-compose.override.yml -f docker-compose.host-proxy.yml "$@"
        else
            docker compose --env-file .env -f docker-compose.yml -f docker-compose.host-proxy.yml "$@"
        fi
    else
        docker compose --env-file .env "$@"
    fi
}
compose config --quiet

printf '拉取 MeldMail 镜像…\n'
# A failed pull exits before any running container is replaced.
compose pull meldmail
printf '启动并检查 MeldMail…\n'
if ! compose up --detach --no-build --pull never --wait --wait-timeout 90 meldmail; then
    printf '部署未通过健康检查，未清理镜像。请运行 docker compose logs --tail=50 meldmail 排查。\n' >&2
    exit 1
fi
cleanup_images
compose ps meldmail
printf '部署完成：默认访问 http://127.0.0.1:18085；自定义端口请查看上方映射（MELDMAIL_HTTP_PORT）\n'
