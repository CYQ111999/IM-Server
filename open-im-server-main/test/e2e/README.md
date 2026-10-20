# OpenIM 端到端（E2E）测试模块

## 概述

本目录是 OpenIM 的端到端（E2E）测试套件。E2E 测试用于模拟真实使用场景，验证 OpenIM 各组件在集成环境下能否正常工作。

测试覆盖 API 接口、聊天服务、Web 界面、RPC 组件，以及不同负载下的性能与扩展能力。

## 目录结构

```bash
test/e2e/
├── conformance/             # 验证 OpenIM API 行为一致性的测试
├── framework/               # 构建与运行 E2E 测试所用的辅助代码
│   ├── config/              # 测试配置与管理
│   ├── ginkgowrapper/       # 包装测试库，统一处理失败与跳过
│   └── helpers/             # 辅助函数，例如创建用户、发送消息等
├── api/                     # OpenIM API 的端到端测试
├── chat/                    # 业务服务测试（登录、注册等逻辑）
├── web/                     # Web 前端测试（登录、注册、消息收发）
├── rpc/                     # 各 RPC 组件的端到端测试
│   ├── auth/                # 认证服务测试
│   ├── conversation/        # 会话管理测试
│   ├── friend/              # 好友关系测试
│   ├── group/               # 群组管理测试
│   └── message/             # 消息处理测试
├── scalability/             # OpenIM 扩展能力测试
├── performance/             # 性能测试，例如负载测试与压力测试
└── upgrade/                 # 升级过程中的兼容性与稳定性测试
```

各目录说明：

- `conformance/`：验证 OpenIM API 实现是否符合约定的测试。
- `framework/`：基于 Ginkgo 框架构建与运行 E2E 测试的辅助代码。
  - `config/`：管理测试配置与选项。
  - `ginkgowrapper/`：对 Ginkgo 的 `Fail`、`Skip` 做包装，以便处理结构化数据引发的 panic。
  - `helpers/`：通用测试动作的工具函数，例如创建用户、投递消息等。
- `api/`：OpenIM API 接口的 E2E 测试。
- `chat/`：聊天服务测试，包含鉴权、会话管理与消息逻辑。
- `web/`：Web 界面测试，包含用户交互与信息交换。
- `rpc/`：各 RPC 组件的 E2E 测试（`auth`、`conversation`、`friend`、`group`、`message`）。
- `scalability/`：OpenIM 系统的扩展能力测试。
- `performance/`：性能测试，包含负载测试与压力测试。
- `upgrade/`：升级流程测试，关注兼容性与稳定性。

## 前置条件

OpenIM 的运行依赖 Mongo、Kafka 等组件，因此运行 E2E 测试前需要先把依赖环境启动起来：

```bash
docker compose up -d
```

也可以使用 [kubernetes 部署方式](https://github.com/openimsdk/helm-charts)。

运行前请确认已安装：

- Docker
- Kubernetes
- Ginkgo 测试框架
- Go（1.19 及以上）

## 配置

测试配置位于 `config/` 目录，格式为 YAML，可设置 API 地址、用户凭据、测试数据等参数。

## 运行测试

运行单个或一组测试需要本机已安装 [Ginkgo](https://github.com/onsi/ginkgo)：

```
ginkgo --help
  --focus value
    	设置后，Ginkgo 只运行匹配该正则的用例，可多次指定，多个取值之间为“或”的关系。
```

运行全部 E2E 测试：

```sh
ginkgo -v --randomizeAllSpecs --randomizeSuites --failOnPending --cover --trace --race --progress
```

也可以指定测试目录，只运行某个测试或某组测试：

```bash
ginkgo -v ./test/e2e/chat
```

或通过 Makefile 运行：

```bash
make test-e2e
```

## 编写测试

1. 进入 `test/e2e/` 目录。
2. 新增测试文件，或修改已有测试以覆盖新场景。
3. 按 Ginkgo 的 BDD 风格编写用例，保证描述清晰。
4. 本地运行并通过测试。
5. 提交改动。

## 反馈问题

运行 E2E 测试时若遇到问题，请在 GitHub 仓库提交 issue，并附上：

- 清晰简洁的问题描述。
- 复现步骤。
- 相关日志与测试输出。
- 其他有助于排查的信息。
