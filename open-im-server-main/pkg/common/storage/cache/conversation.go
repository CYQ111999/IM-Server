package cache

import (
	"context"

	relationtb "github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
)

// 当缓存中无数据时，将执行传入的 fn 回源函数。
type ConversationCache interface {
	BatchDeleter
	CloneConversationCache() ConversationCache
	// 从缓存获取用户的会话 ID 列表
	GetUserConversationIDs(ctx context.Context, ownerUserID string) ([]string, error)
	GetUserNotNotifyConversationIDs(ctx context.Context, userID string) ([]string, error)
	GetPinnedConversationIDs(ctx context.Context, userID string) ([]string, error)
	DelConversationIDs(userIDs ...string) ConversationCache

	GetUserConversationIDsHash(ctx context.Context, ownerUserID string) (hash uint64, err error)
	DelUserConversationIDsHash(ownerUserIDs ...string) ConversationCache

	// 从缓存获取单个会话
	GetConversation(ctx context.Context, ownerUserID, conversationID string) (*relationtb.Conversation, error)
	DelConversations(ownerUserID string, conversationIDs ...string) ConversationCache
	DelUsersConversation(conversationID string, ownerUserIDs ...string) ConversationCache
	// 从缓存获取单个会话
	GetConversations(ctx context.Context, ownerUserID string,
		conversationIDs []string) ([]*relationtb.Conversation, error)
	// 从缓存获取一个用户的全部会话
	GetUserAllConversations(ctx context.Context, ownerUserID string) ([]*relationtb.Conversation, error)
	// 从缓存获取用户会话的接收消息
	GetUserRecvMsgOpt(ctx context.Context, ownerUserID, conversationID string) (opt int, err error)
	DelUserRecvMsgOpt(ownerUserID, conversationID string) ConversationCache
	// 获取超级群中接收消息但无需通知的 userID 列表
	// GetSuperGroupRecvMsgNotNotifyUserIDs(ctx context.Context, groupID string) (userIDs []string, err error)
	DelSuperGroupRecvMsgNotNotifyUserIDs(groupID string) ConversationCache
	// 获取超级群中接收消息但无需通知的 userID 列表的哈希
	// GetSuperGroupRecvMsgNotNotifyUserIDsHash(ctx context.Context, groupID string) (hash uint64, err error)
	DelSuperGroupRecvMsgNotNotifyUserIDsHash(groupID string) ConversationCache

	// GetUserAllHasReadSeqs(ctx context.Context, ownerUserID string) (map[string]int64, error)
	DelUserAllHasReadSeqs(ownerUserID string, conversationIDs ...string) ConversationCache

	GetConversationNotReceiveMessageUserIDs(ctx context.Context, conversationID string) ([]string, error)
	DelConversationNotReceiveMessageUserIDs(conversationIDs ...string) ConversationCache
	DelConversationNotNotifyMessageUserIDs(userIDs ...string) ConversationCache
	DelUserPinnedConversations(userIDs ...string) ConversationCache
	DelConversationVersionUserIDs(userIDs ...string) ConversationCache

	FindMaxConversationUserVersion(ctx context.Context, userID string) (*relationtb.VersionLog, error)
}
