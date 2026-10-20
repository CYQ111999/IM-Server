# 编译与镜像构建

本项目提供两种方式产出可运行的程序与镜像：

1. **本地编译**（日常开发）：用 Mage 直接编译出二进制；
2. **容器化构建镜像**（需要分发时）：用 `build/` 下的脚本配合 Docker 构建。

## 一、本地编译（推荐）

本项目没有 `Makefile`，构建统一走 Mage（定义在根目录 `magefile.go`）。

```bash
# 编译全部服务，产物输出到 _output/bin/
mage build

# 指定自定义配置目录编译
mage buildWithCustomConfig

# 启动 / 停止（standalone 单机模式）
mage start
mage stop
```

编译完成后 `_output/bin/` 下会得到 `openim-api`、`openim-msggateway`、`openim-msgtransfer`、`openim-push`、`openim-crontask` 以及各 `openim-rpc-*` 可执行文件。

> 也可直接使用 Go 原生命令：`go build -o _output/bin/openim-api ./cmd/openim-api`。

## 二、构建容器镜像

### 目录结构

```
build/
├── build.sh                  # 构建全部服务的镜像
├── build_single.sh           # 只构建指定服务的镜像
├── goreleaser.yaml           # 发布配置
└── images/openim-server/
    ├── Dockerfile            # 镜像构建文件
    └── docker-compose.build.yml
```

### 前置要求

- Docker（建议 20.10+），并确认已安装 buildx 插件（`~/.docker/cli-plugins/docker-buildx` 存在）。
- 若在 macOS 上构建，Docker 虚拟机内存建议不低于 4GB，否则容易 OOM。
- 需要多架构镜像时，确保已启用 `docker buildx` 的对应平台模拟（QEMU）。

### 构建全部服务

```bash
cd build
./build.sh
```

### 只构建单个服务

```bash
cd build
SERVICE=openim-api ./build_single.sh
```

### 可用环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `SERVICE` | 仅 `build_single.sh`：指定要构建的服务名 | 空（全部） |
| `PLATFORMS` | 目标平台，多个用逗号分隔 | `linux/amd64,linux/arm64` |
| `IMAGE_TAGS` | 自定义镜像标签，多个用逗号分隔 | 空 |
| `IMAGE_REGISTRIES` | 镜像仓库地址，多个用逗号分隔 | 空 |
| `RELEASE` | 是否为发布构建 | `false` |
| `PUSH` | 构建后是否推送镜像 | `false` |
| `DRY_RUN` | 只打印命令不实际执行 | `false` |

示例：构建并推送 arm64 镜像到私有仓库

```bash
cd build
PLATFORMS=linux/arm64 \
IMAGE_REGISTRIES=registry.example.com/im \
IMAGE_TAGS=v3.8.0 \
PUSH=true \
./build.sh
```

### 构建产物

- 二进制产物统一输出到仓库根目录的 `_output/bin/`；
- 镜像则保存在本地 Docker 镜像库（或按 `PUSH=true` 推送到仓库）。

## 三、上游文档的差异说明

上游 OpenIM 的 `build/README.md` 描述的是基于 `make image`、`build/build-image/Dockerfile`、`common.sh` 的容器内编译流程（数据容器 + rsync 容器 + 构建容器三段式）。**这套流程在本项目中已被移除**，对应的 Makefile、`build-image`、`common.sh` 均不存在，请勿按上游文档操作。

当前实际可用的就是上文的两条路径：`mage build` 与 `build/build.sh`。
