# 飞牛 fpk 接入自动更新：方案（未实施）

状态：**方案，没有改动内测 fpk 的任何文件**。这里的每一步都要在一台真实的飞牛机器上装一遍才算数。

## 现在的情况

- fpk 用 `docker load` 装入自带的镜像，Compose 项目名 `nassimhub-beta`，用 `--profile app` 启动，
  本地 tag 是 `nassimhub-beta-{api,web,hardware}:0.9.0`，服务是 `api`、`web`、`hardware`、`api-init`，
  每个服务都写了 `pull_policy: never`。
- **没有 updater 服务**，所以装 fpk 的机器收不到 `deploy/RELEASING.md` 发出去的任何版本；
  卡片上只会显示「更新器未运行」。
- 190 那台跑的是 `deploy/fnos/compose.fnos.yml`（项目 `nas-comm-gateway`），也没有 updater，情况一样。

## 为什么用 retag 策略

updater 原来只会一种安装方式（env）：把带 digest 的镜像引用写进部署目录的 `.env`，再 `compose up`。
fpk 的 Compose 文件写死了本地 tag，也没有这样一个 `.env`，所以这种方式用不上。

r114 加了第二种方式 `retag`，是专门给 fpk 这类部署做的：

1. 按 digest 拉镜像（先 ghcr，失败再试 `MODEMDECK_REGISTRY_MIRRORS`），Docker 自己校验 digest；
2. 记下每个 slot（比如 `nassimhub-beta-api:0.9.0`）当前指向的镜像 ID；
3. `docker tag <新镜像ID> nassimhub-beta-api:0.9.0`；
4. `docker compose -p nassimhub-beta -f <fpk 的 compose> --profile app up -d --no-deps --wait <服务>`；
5. 失败就把 slot tag 回原来的镜像 ID，再 up 一次。

`pull_policy: never` 和这套流程不冲突：slot tag 在 up 之前已经指向新镜像了，Compose 不需要拉取。
fpk 里的 Compose 文件一个字都不用改，只需要加服务。

## 需要加到 fpk 里的东西

1. **updater 镜像**：跟另外三个一样 `docker load` 进去，tag 成 `nassimhub-beta-updater:0.9.0`。
   retag 要求四个 slot 都有，缺了会直接拒绝启动，免得装一半。
2. **updater 服务**（`profiles: [app]`），要点：
   - 挂 `/var/run/docker.sock`，不映射任何端口，只在 Compose 内网里给 api 用；
   - 根文件系统只读，`cap_drop: [ALL]`，`no-new-privileges`，写状态用一个具名卷；
   - 把 fpk 的应用目录**按原路径**挂进来：`MODEMDECK_DEPLOYMENT_DIR` 和 `MODEMDECK_HOST_DEPLOYMENT_DIR`
     设成同一个宿主机路径。安装时 updater 会另起一个 worker 容器去跑 `docker compose`，
     Compose 文件里的相对路径是在 worker 里解析、再交给宿主机的 Docker 去挂载的，
     两边路径不一致就会挂错目录。
   - 环境变量：

     ```
     MODEMDECK_UPDATE_STRATEGY=retag
     MODEMDECK_COMPOSE_PROJECT=nassimhub-beta
     MODEMDECK_COMPOSE_FILES=<fpk 应用目录>/docker-compose.yml
     MODEMDECK_COMPOSE_PROFILES=app
     MODEMDECK_UPDATE_SLOTS=api=nassimhub-beta-api:0.9.0,web=nassimhub-beta-web:0.9.0,hardware=nassimhub-beta-hardware:0.9.0,updater=nassimhub-beta-updater:0.9.0
     MODEMDECK_UPDATE_SERVICES=api=api,web=web,hardware=hardware,updater=updater
     MODEMDECK_UPDATER_SELF_IMAGE=nassimhub-beta-updater:0.9.0
     MODEMDECK_UPDATER_STATE_VOLUME=<实际的卷名，带项目前缀>
     MODEMDECK_UPDATER_TOKEN_FILE=/run/nassimhub/updater-token
     ```

3. **api 服务**加两项：`MODEMDECK_UPDATER_URL=http://updater:8081`，`MODEMDECK_UPDATER_TOKEN_FILE` 指向同一个口令文件。
4. **口令的生成放在 `api-init` 里**：第一次启动时生成 32 字节随机数写进一个共享卷（权限 0600），
   已经存在就不动。api 和 updater 都只读它。这样 fpk 包里不带任何口令，每台机器各不相同。

## 必须先想清楚的一件事：重装 fpk 会降级

fpk 每次安装或升级都会 `docker load` 自带镜像并打上同样的 slot tag。
假如一台机器已经通过自动更新装到了 v1.17，再装一个打包时是 v1.16 的 fpk，slot 就被 tag 回 v1.16，
而且没有任何提示。

建议 fpk 的安装脚本在 `docker load` 之后、打 tag 之前，读 updater 状态卷里的 `installed-version`：
比 fpk 自带的新就跳过打 tag，只更新 fpk 自身的文件。这一步是 shell 脚本，要在飞牛上实测。

## 版本号要对齐

updater 判断「当前是哪个版本」，先看状态卷里的 `installed-version`（每次安装后写），
没有的话用 updater 镜像编译进去的版本号。fpk 目前的 tag 是 `0.9.0`，而仓库 `VERSION` 是 `1.15.0`。
fpk 应该从一个发布 tag 打包，让编译进镜像的版本号就是那次发布的版本。否则第一次检查会把
自己已经在跑的版本当成新版本，多提示一次「有更新」。装下去也不会装错，只是白拉一遍。

另外，fpk 自带的镜像是 `docker load` 进来的，没有仓库 digest，updater 没法确认它们和发布里的是同一份。
所以**第一次**更新会把四个组件都按 digest 拉一遍、各重建一次。之后 updater 的镜像账本（状态卷里的
`images.json`）有了记录，就只动真正变了的组件。第一次更新的下载量会比较大，这一点要写进发布说明。

## 在飞牛上的验收步骤

1. 装带 updater 的 fpk，卡片不再显示「更新器未运行」，能查到版本（或者在还没发布时显示「查不到版本信息」）。
2. 发一个测试版本：自动下载完成，卡片变绿，按「立即更新」。这是第一次更新，四个容器都会重建（见上一节）。
3. 再发一个只改了 web 的测试版本，更新后只有 web 容器被重建；在飞牛的 Docker 界面确认
   `nassimhub-beta-web:0.9.0` 指向新镜像 ID，其余三个没变。
4. 人为让 web 健康检查失败（比如发一个起不来的测试版），确认会回滚到原镜像 ID。
5. 重新安装同一个 fpk，确认没有被 tag 回旧镜像（上一节的保护）。
6. 断网状态下确认卡片说明原因，不影响其他功能。
