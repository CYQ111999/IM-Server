# 关于 go workspace 的说明

OpenIM 使用了 go1.18 的 [workspace 特性](https://go.dev/doc/tutorial/workspaces)。新增模块后，需要在根目录执行 `go work use -r .`，让 workspace 保持同步。

### 新建一个工具

1. 先在 `/tools` 下创建以工具名命名的目录，并进入该目录。
2. 初始化工程。
3. 在当前目录执行 `go work use -r .` 更新 workspace。
4. 编写你的工具。

可以执行下面的命令完成上述步骤：

```bash
# 把 CRD_NAME 和 CRD_GROUP 改成你自己的名称
export OPENIM_TOOLS_NAME=<Changeme>

# 复制粘贴执行，创建新的工具模块
mkdir tools/${OPENIM_TOOLS_NAME}
cd tools/${OPENIM_TOOLS_NAME}
go mod init github.com/openimsdk/open-im-server/tools/${OPENIM_TOOLS_NAME}
go mod tidy
go work use -r .
cd ../..
```
