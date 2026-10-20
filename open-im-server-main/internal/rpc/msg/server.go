package msg

import (
	"context"

	"google.golang.org/grpc"

	"github.com/openimsdk/open-im-server/v3/pkg/dbbuild"
	"github.com/openimsdk/open-im-server/v3/pkg/mqbuild"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcli"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache/redis"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/database/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/webhook"
	"github.com/openimsdk/open-im-server/v3/pkg/notification"
	"github.com/openimsdk/open-im-server/v3/pkg/rpccache"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/conversation"
	"github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/discovery"
)

type MessageInterceptorFunc func(ctx context.Context, globalConfig *Config, req *msg.SendMsgReq) (*sdkws.MsgData, error)

// MessageInterceptorChain 定义由多个消息拦截器函数组成的责任链。
type MessageInterceptorChain []MessageInterceptorFunc

type Config struct {
	RpcConfig          config.Msg
	RedisConfig        config.Redis
	MongodbConfig      config.Mongo
	KafkaConfig        config.Kafka
	NotificationConfig config.Notification
	Share              config.Share
	WebhooksConfig     config.Webhooks
	LocalCacheConfig   config.LocalCache
	Discovery          config.Discovery
}

// msgServer 封装了消息处理所需的各项依赖。
type msgServer struct {
	msg.UnimplementedMsgServer
	RegisterCenter         discovery.Conn                   // 服务注册中心，用于服务注册与发现。
	MsgDatabase            controller.CommonMsgDatabase     // 消息数据库操作接口。
	UserLocalCache         *rpccache.UserLocalCache         // 用户信息本地缓存。
	FriendLocalCache       *rpccache.FriendLocalCache       // 好友信息本地缓存。
	GroupLocalCache        *rpccache.GroupLocalCache        // 群组信息本地缓存。
	ConversationLocalCache *rpccache.ConversationLocalCache // 会话信息本地缓存。
	Handlers               MessageInterceptorChain          // 消息处理拦截器链。
	notificationSender     *notification.NotificationSender // 用于发送通知的 RPC 客户端。
	msgNotificationSender  *MsgNotificationSender           // 用于发送消息通知的 RPC 客户端。
	config                 *Config                          // 全局配置。
	webhookClient          *webhook.Client
	conversationClient     *rpcli.ConversationClient

	adminUserIDs []string
}

func (m *msgServer) addInterceptorHandler(interceptorFunc ...MessageInterceptorFunc) {
	m.Handlers = append(m.Handlers, interceptorFunc...)

}

func Start(ctx context.Context, config *Config, client discovery.SvcDiscoveryRegistry, server grpc.ServiceRegistrar) error {
	dbb := dbbuild.NewBuilder(&config.MongodbConfig, &config.RedisConfig)
	mgocli, err := dbb.Mongo(ctx)
	if err != nil {
		return err
	}
	rdb, err := dbb.Redis(ctx)
	if err != nil {
		return err
	}
	builder, err := mqbuild.NewBuilder(config.Share.Queue, &config.KafkaConfig, rdb)
	if err != nil {
		return err
	}
	redisProducer, err := builder.GetTopicProducer(ctx, mqbuild.TopicToRedis)
	if err != nil {
		return err
	}
	msgDocModel, err := mgo.NewMsgMongo(mgocli.GetDB())
	if err != nil {
		return err
	}
	msgModel := redis.NewMsgCache(rdb, msgDocModel)
	seqConversation, err := mgo.NewSeqConversationMongo(mgocli.GetDB())
	if err != nil {
		return err
	}
	seqConversationCache := redis.NewSeqConversationCacheRedis(rdb, seqConversation)
	seqUser, err := mgo.NewSeqUserMongo(mgocli.GetDB())
	if err != nil {
		return err
	}
	seqUserCache := redis.NewSeqUserCacheRedis(rdb, seqUser)
	userConn, err := client.GetConn(ctx, config.Discovery.RpcService.User)
	if err != nil {
		return err
	}
	groupConn, err := client.GetConn(ctx, config.Discovery.RpcService.Group)
	if err != nil {
		return err
	}
	friendConn, err := client.GetConn(ctx, config.Discovery.RpcService.Friend)
	if err != nil {
		return err
	}
	conversationConn, err := client.GetConn(ctx, config.Discovery.RpcService.Conversation)
	if err != nil {
		return err
	}
	conversationClient := rpcli.NewConversationClient(conversationConn)
	msgDatabase := controller.NewCommonMsgDatabase(msgDocModel, msgModel, seqUserCache, seqConversationCache, redisProducer)
	s := &msgServer{
		MsgDatabase:            msgDatabase,
		RegisterCenter:         client,
		UserLocalCache:         rpccache.NewUserLocalCache(rpcli.NewUserClient(userConn), &config.LocalCacheConfig, rdb),
		GroupLocalCache:        rpccache.NewGroupLocalCache(rpcli.NewGroupClient(groupConn), &config.LocalCacheConfig, rdb),
		ConversationLocalCache: rpccache.NewConversationLocalCache(conversationClient, &config.LocalCacheConfig, rdb),
		FriendLocalCache:       rpccache.NewFriendLocalCache(rpcli.NewRelationClient(friendConn), &config.LocalCacheConfig, rdb),
		config:                 config,
		webhookClient:          webhook.NewWebhookClient(config.WebhooksConfig.URL),
		conversationClient:     conversationClient,
		adminUserIDs:           config.Share.IMAdminUser.UserIDs,
	}

	s.notificationSender = notification.NewNotificationSender(&config.NotificationConfig, notification.WithLocalSendMsg(s.SendMsg))
	s.msgNotificationSender = NewMsgNotificationSender(config, notification.WithLocalSendMsg(s.SendMsg))

	msg.RegisterMsgServer(server, s)

	return nil
}

func (m *msgServer) conversationAndGetRecvID(conversation *conversation.Conversation, userID string) string {
	if conversation.ConversationType == constant.SingleChatType ||
		conversation.ConversationType == constant.NotificationChatType {
		if userID == conversation.OwnerUserID {
			return conversation.UserID
		} else {
			return conversation.OwnerUserID
		}
	} else if conversation.ConversationType == constant.ReadGroupChatType {
		return conversation.GroupID
	}
	return ""
}
