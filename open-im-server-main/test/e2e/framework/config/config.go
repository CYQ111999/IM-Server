package config

import (
	"flag"
	"os"
)

// Flags 是 AddOptions 注册参数所用的 flag 集合。编写测试时应使用它，
// 而不是直接往全局命令行上添加参数。
var Flags = flag.NewFlagSet("", flag.ContinueOnError)

// CopyFlags 保证源 flag 集合中定义的所有参数都出现在目标集合中，
// 效果等同于直接在目标集合中定义。沿用 flag 包的行为：
// 若目标集合已存在同名参数，则会 panic。
func CopyFlags(source *flag.FlagSet, target *flag.FlagSet) {
	source.VisitAll(func(flag *flag.Flag) {
		// 这里无需复制 flag.DefValue。原始默认值（例如 flag.String 设置的）
		// 已经保存在取值中，Var 在生成帮助信息时会自行提取。
		target.Var(flag.Value, flag.Name, flag.Usage)
	})
}

// Config 定义 OpenIM 各组件的配置结构。
type Config struct {
	APIHost             string
	APIPort             string
	MsgGatewayHost      string
	MsgTransferHost     string
	PushHost            string
	RPCAuthHost         string
	RPCConversationHost string
	RPCFriendHost       string
	RPCGroupHost        string
	RPCMsgHost          string
	RPCThirdHost        string
	RPCUserHost         string
	// 按需补充其他配置字段
}

// LoadConfig 从环境变量或默认值加载配置。
func LoadConfig() *Config {
	return &Config{
		APIHost: getEnv("OPENIM_API_HOST", "127.0.0.1"),
		APIPort: getEnv("API_OPENIM_PORT", "10002"),

		// TODO: 设置默认值
		MsgGatewayHost:      getEnv("OPENIM_MSGGATEWAY_HOST", "default-msggateway-host"),
		MsgTransferHost:     getEnv("OPENIM_MSGTRANSFER_HOST", "default-msgtransfer-host"),
		PushHost:            getEnv("OPENIM_PUSH_HOST", "default-push-host"),
		RPCAuthHost:         getEnv("OPENIM_RPC_AUTH_HOST", "default-rpc-auth-host"),
		RPCConversationHost: getEnv("OPENIM_RPC_CONVERSATION_HOST", "default-rpc-conversation-host"),
		RPCFriendHost:       getEnv("OPENIM_RPC_FRIEND_HOST", "default-rpc-friend-host"),
		RPCGroupHost:        getEnv("OPENIM_RPC_GROUP_HOST", "default-rpc-group-host"),
		RPCMsgHost:          getEnv("OPENIM_RPC_MSG_HOST", "default-rpc-msg-host"),
		RPCThirdHost:        getEnv("OPENIM_RPC_THIRD_HOST", "default-rpc-third-host"),
		RPCUserHost:         getEnv("OPENIM_RPC_USER_HOST", "default-rpc-user-host"),
	}
}

// getEnv 是读取环境变量、取不到则返回默认值的辅助函数。
func getEnv(key, defaultValue string) string {
	value, exists := os.LookupEnv(key)
	if !exists {
		return defaultValue
	}
	return value
}
