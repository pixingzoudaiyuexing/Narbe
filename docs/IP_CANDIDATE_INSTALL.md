# 国内 IP 功能候选离线安装包

这是未发布的开发候选，不是正式版本、GHCR `latest`、Git Tag 或 Release。CI 在 Linux AMD64 和 ARM64 原生 runner 分别完成 manager 容器运行验证、Probe 容器验证和 beta.5 到候选的隔离升级验证后，上传 `narbe-candidate-linux-{amd64,arm64}` Artifact（保留 14 天）。每个 Artifact 含对应架构的 `narbe-candidate-linux-<arch>-<source-sha-prefix>.tar.gz`、`install-candidate.py` 和 `SHA256SUMS`；压缩包内有两个镜像的 `docker save`、镜像 inspect、完整 `SOURCE_SHA`、内层 `SHA256SUMS` 和无构建步骤的 Compose 文件。没有推送候选到公共 Registry。

只从本仓库对应源码提交的成功 CI Run 下载 Artifact。先从 GitHub Run 页面核对完整 40 位 commit SHA 与目标服务器架构；不要只凭文件名或下载链接判断来源。解压 GitHub Artifact 后不要编辑包内文件，在 Artifact 目录执行以下命令。校验先验证外层和内层 SHA256、精确文件集、镜像标签、OCI revision、架构和 `docker save` 内标签。安装器区分镜像 Config ID 与 OCI Manifest Digest，核对配置、Manifest 对配置的引用及每层有序内容哈希；安装时还会在 `docker load` 后回读允许的镜像 ID、完整容器配置和 RootFS，兼容 Docker Engine 与 Docker Desktop 的不同 ID 展示方式。需要 Python 3、Docker Engine 和 Docker Compose Plugin；服务器不需要 Go 编译环境。

```bash
python3 install-candidate.py --bundle ./narbe-candidate-linux-amd64-<sha前12位>.tar.gz \
  --expect-sha <GitHub-Run-完整40位源码SHA> --mode fresh \
  --install-dir /opt/narbe-candidate --check-only
```

`--check-only` 只核对下载包，不调用 Docker。实际全新安装在独立目录和 `narbe-candidate` 项目、`narbe-candidate_narbe_data` Volume 中进行；若这些资源已存在则停止。默认只绑定 `127.0.0.1:8080`。首次管理员密码会在终端无回显输入两遍，至少 12 字符；自动化测试可传只读私有 `--password-file /安全路径/密码文件`，文件必须为普通文件且权限至多 `0600`，不会写入包、命令行或日志。远程访问请在可信 HTTPS 反向代理下设置精确 Origin，管理端仍只绑定 loopback：

```bash
python3 install-candidate.py --bundle ./narbe-candidate-linux-amd64-<sha前12位>.tar.gz \
  --expect-sha <完整40位SHA> --mode fresh --install-dir /opt/narbe-candidate \
  --port 8080 --origin https://narbe.example.com
```

已有 beta.5 可用 `--mode upgrade --install-dir /原有beta5安装目录`。安装器只接受仓库原版 beta.5 单一 Compose、锁定的 beta.5 Digest、当前正在运行的 `narbe` 项目及 `narbe_narbe_data` 所属 Volume；逐项核对真实容器、端口映射和 Origin。它停止原服务后将旧 Compose、`.env`、`image.env`、管理员密码文件及 `/data` 的 SQLite/WAL、`master.key` 完整复制到私有备份目录，确认关键文件非空后只更新 `image.env` 为固定源码 SHA 的本地候选镜像，再以原目录和 Volume 启动。旧密码、账号密文和网络代理配置继续由原数据保存，不会重新设置管理员密码。备份路径会打印并写入 `candidate-install.json`。升级前仍应由操作者额外保留完整安装目录和数据 Volume 的独立备份；旧 beta.5 不识别新 schema，回退必须恢复升级前的数据副本，不能仅切回旧镜像。

对于定制 Compose、多 Compose 文件、bind mount 数据目录、不同项目名、额外环境覆盖或无法核实的旧容器，自动升级会拒绝。此时应先人工盘点真实路径、Volume、Origin、端口和备份方式，不能把默认脚本套在已有业务部署上。安装器不会运行 `down -v`、删除 Volume、覆盖未知文件或自动部署到 Owner 环境。实际升级仍可能迁移 SQLite schema；运行前请确认已完成独立备份与维护窗口。

Probe 需在国内 VPS 或预先核实实际出口的机器上单独安装，选择对应架构包，并从管理端「国内连通性」生成一次性注册凭据，保存为私有 `0600` 文件。目标目录必须是全新的 `/opt/narbe-probe-candidate` 或其他以 `narbe-probe-candidate` 结尾的绝对目录。Probe 不包含云密钥、不开放入站端口，控制面要求可信 HTTPS：

```bash
python3 install-candidate.py --bundle ./narbe-candidate-linux-amd64-<sha前12位>.tar.gz \
  --expect-sha <完整40位SHA> --mode probe \
  --install-dir /opt/narbe-probe-candidate \
  --control-url https://narbe.example.com \
  --registration-file /安全路径/probe-registration-token
```

安装成功后到管理端检查登录、账户、探针的实际出口国家和公网 IPv4，再用**已授权的具体公网资源**验证检测结果。CI、HTTP 测试替身和离线安装不能证明真实四云写入或中国大陆网络可达性。
