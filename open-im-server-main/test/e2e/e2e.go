package e2e

import (
	"testing"

	gettoken "github.com/openimsdk/open-im-server/v3/test/e2e/api/token"
	"github.com/openimsdk/open-im-server/v3/test/e2e/api/user"
)

// RunE2ETests 先校验配置参数（通过命令行 flag 传入），再使用 Ginkgo 运行 E2E 测试。
// 若指定了“报告目录”，该目录下会生成一个或多个 JUnit 测试报告，集群日志也会一并保存。
// 并行模式下，该函数会在每个 Ginkgo 节点上被调用。
func RunE2ETests(t *testing.T) {

	// 新函数的用法示例
	token, _ := gettoken.GetUserToken("openIM123456")

	// 示例：获取用户信息
	_ = user.GetUsersInfo(token, []string{"user1", "user2"})

	// 示例：更新用户信息
	_ = user.UpdateUserInfo(token, "user1", "NewNickname", "https://github.com/openimsdk/open-im-server/blob/main/assets/logo/openim-logo.png")

	// 示例：获取用户在线状态
	_ = user.GetUsersOnlineStatus(token, []string{"user1", "user2"})

	// 示例：强制用户下线
	_ = user.ForceLogout(token, "4950983283", 2)

	// 示例：校验用户账号
	_ = user.CheckUserAccount(token, []string{"openIM123456", "anotherUserID"})

	// 示例：批量获取用户
	_ = user.GetUsers(token, 1, 100)
}
