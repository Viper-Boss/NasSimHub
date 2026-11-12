# 发布流程

一次发布分四步，只有第三步需要签名私钥，而私钥只在控制台那台电脑上：

```
打 tag  ──►  GitHub Actions 构建并推送镜像  ──►  控制台签名  ──►  放进公开的发布仓库
 (你)           (自动，产出 release.unsigned.json)     (你，离线)        (你，git push)
                                                                              │
                              网关每 6 小时左右看一次，验签通过才下载  ◄──────┘
                              默认自动下载；安装要在「软件更新」卡片上手动按一下
```

## 这几个文件是什么

| 文件 | 谁产出 | 内容 | 能不能公开 |
|---|---|---|---|
| `release.unsigned.json` | Actions 的 `manifest` 任务 | 版本号 + 5 个镜像（api、web、hardware、updater、cloudflared）各自的 `sha256` digest | 能，本来就要拿去签 |
| `release.signed.json` | 控制台「发布签名」 | 同一份清单 + 控制台私钥的 Ed25519 签名 | **必须公开**，网关要能匿名下载 |
| `issuer.key` | 控制台 | 签名私钥 | **绝不**，不进任何仓库 |

网关只认签名，不认地址：清单放在哪儿、从哪个镜像站来都无所谓，验不过就当没看见。
所以发布仓库可以公开，也可以随便加镜像站。

## 一次性准备

1. **建公开的发布仓库** `Viper-Boss/nassimhub-releases`（名字不要改，网关里编进去的默认地址就是它）。
   把本次交付里 `_发布仓库脚手架/nassimhub-releases/README.md` 放进去作为第一个提交。
   第一次正式发布之前 `stable.signed.json` 不存在，网关的软件更新卡片显示「查不到版本信息」是正常的。

2. **第一次发布成功后，把 4 个镜像包设成公开。** 从私有仓库推上去的包默认是私有的，网关匿名拉不下来。
   在 GitHub 上打开 Viper-Boss 的 Packages 页，分别进入 `nassimhub`、`nassimhub-web`、
   `nassimhub-hardware`、`nassimhub-updater`，在包设置（Package settings）里把可见性改成 Public。
   这一步只能你在网页上点，我做不了。
   公开的只是编译好的镜像。源码仓库仍然是私有的。

3. **可选：国内镜像**，见文末「给国内网关加镜像」。

## 每次发布

1. **改版本号。** `VERSION` 里写 `1.16.0`（不带 v），提交。

2. **打带注释的 tag 并推送。** tag 必须是 `v` + `VERSION`，而且必须是 annotated tag：

   ```sh
   git tag -a v1.16.0 -m v1.16.0
   git push origin v1.16.0
   ```

   工作流会拒绝轻量 tag，也会拒绝和 `VERSION` 对不上的 tag。

3. **等 Actions 跑完**（仓库的 Actions 页 → Publish release images）。它会做三件事：
   - **校验**：tag 和 `VERSION` 一致、是带注释的 tag；再去 ghcr 查上一个版本的镜像在不在。
     第一次发布时上一个 tag 是上游项目的，镜像不在这个命名空间里，于是四个组件全部重新构建。
   - **构建并推送**：只重建有改动的组件，没改的直接把上一版镜像重新打上新 tag（digest 不变）。
     推送用的是工作流自带的 `GITHUB_TOKEN`，不需要你配置任何凭据。
   - **记录清单**：生成 `release.unsigned.json`，作为这次运行的 artifact（名字 `release-unsigned-<tag>`）上传。
     工作流**不签名**，因为它拿不到私钥，也不应该拿到。

4. **下载 `release.unsigned.json`**：Actions 运行页底部的 Artifacts 里下载 `release-unsigned-v1.16.0`（保留 90 天），解压得到 `release.unsigned.json`。

5. **在控制台签名**：打开控制台 →「发布签名」→ 粘贴文件内容 → 看预览里的版本和 5 个 digest
   → 需要的话填「最低版本」→ 把版本号原样再输一遍 → 签名 → 下载 `release.signed.json`。

   控制台会拦下这些情况：
   - 同一个版本号已经签过、但镜像不一样（一个版本号只能对应一组镜像，请发新版本号）；
   - 比已经签过的版本还低（网关取最高版本，低版本永远不会被装）；
   - 少组件、镜像来自别的仓库、多了不认识的字段（网关会拒收的，这里先拒）。

   每次签出来的文件都会先留档到控制台数据目录的 `releases/` 下，留档写不进去就不给你文件。

6. **放进发布仓库**：

   ```
   nassimhub-releases/
     stable.signed.json            ← 覆盖成这一版（网关看的就是它）
     releases/v1.16.0.signed.json  ← 同一个文件再放一份，留历史
     releases/v1.16.0.md           ← 可选：给人看的更新说明
   ```

   提交并推送。

7. **刷新 jsDelivr 缓存**（不刷也行，最多等半天）：在浏览器里打开
   `https://purge.jsdelivr.net/gh/Viper-Boss/nassimhub-releases@main/stable.signed.json`

之后的事是自动的：网关每 6 小时左右（有随机抖动）去三个地址各取一份，选验签通过的最高版本；
开着「自动下载」的就在后台按 digest 把镜像拉好，卡片变成绿色「立即更新」，等人按。

## 网关从哪里取清单

默认三个地址，同时去取，谁版本高、验签过就用谁：

- `https://raw.githubusercontent.com/Viper-Boss/nassimhub-releases/main/stable.signed.json`
- `https://cdn.jsdelivr.net/gh/Viper-Boss/nassimhub-releases@main/stable.signed.json`
- `https://fastly.jsdelivr.net/gh/Viper-Boss/nassimhub-releases@main/stable.signed.json`

要加地址（对象存储、局域网里的一个文件都行），设 `MODEMDECK_RELEASE_CHANNEL_URLS`，
逗号分隔。这会**替换**默认列表，所以想保留默认的就把它们也写上。

**另外两个来源：NKN 和 DHT（r121 起）。** 网关还会向其他网关要清单，和上面三个地址一起比版本。
发布时**不用多做任何事**：你自己的网关从 GitHub 拿到新版本后，会自动转交给来问的其他网关，
那些网关再转交给下一批。签名保证了谁转交都一样。细节见 `docs/p2p-updates.md`。

## 给国内网关加镜像

清单很小，jsDelivr 在国内一般能通；慢的是镜像本身。镜像按 digest 拉，Docker 自己校验，
所以镜像站只能「拉不下来」，没法「换成别的东西」。

以阿里云容器镜像服务个人版为例：

1. 建一个命名空间，比如 `nassimhub`，在里面建 4 个仓库，名字必须和 ghcr 上的一样：
   `nassimhub`、`nassimhub-web`、`nassimhub-hardware`、`nassimhub-updater`。
2. 每次发布签名之后，用能同时登录两边的机器把这一版复制过去，**保留多架构和 digest**：

   ```sh
   for image in nassimhub nassimhub-web nassimhub-hardware nassimhub-updater; do
     skopeo copy --all \
       docker://ghcr.io/viper-boss/${image}:v1.16.0 \
       docker://registry.cn-hangzhou.aliyuncs.com/nassimhub/${image}:v1.16.0
   done
   ```

   复制完用 `docker buildx imagetools inspect` 看一眼，digest 必须和 `release.signed.json` 里的一致；
   不一致的话网关会拉取失败并退回 ghcr，不会装错东西，但镜像也就白放了。
3. 网关上设 `MODEMDECK_REGISTRY_MIRRORS=registry.cn-hangzhou.aliyuncs.com/nassimhub`（只对 retag 策略生效）。
   顺序是先 ghcr，失败再试镜像站。

## 注意

- updater 持有 Docker socket，属于宿主机可信计算基；当前签名为 Ed25519。
  发行/许可密钥分离与后量子签名尚未迁移，详见 [安全边界](../docs/SECURITY-BOUNDARIES.md)。

- **Actions 分钟数**：私有仓库有免费额度上限。arm64 是用 QEMU 模拟构建的，比 amd64 慢好几倍；
  只改了前端的版本只会重建 web，花得少；第一次发布四个全建，花得最多。
- **所有基础镜像都钉了 digest**，包括内置 NKN 节点用的 `nknorg/nkn`（`deploy/tests/static-test.sh` 会检查）。
  要升级其中一个，就改 `Dockerfile` 里那一行的 tag 和 digest，别去掉 digest。
  nknorg 只有 `latest` 这一个多架构 tag，新 digest 在 Docker Hub 的
  `https://hub.docker.com/v2/repositories/nknorg/nkn/tags/?name=latest` 里，取最外层的 `digest`。
- **artifact attestation**（构建来源证明）只在公开仓库里做：GitHub 只给企业版的私有仓库提供这个功能，
  否则这一步会让整次发布失败。网关不依赖它。
- **飞牛 fpk 目前没有 updater**，装 fpk 的机器收不到这里发布的更新。接入方案见 `docs/fpk-updater-proposal.md`。
