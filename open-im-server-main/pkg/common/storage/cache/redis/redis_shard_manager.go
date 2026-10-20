package redis

import (
	"context"

	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

const (
	defaultBatchSize       = 50
	defaultConcurrentLimit = 3
)

// RedisShardManager 用于对 key 进行分片处理的类型
type RedisShardManager struct {
	redisClient redis.UniversalClient
	config      *Config
}
type Config struct {
	batchSize       int
	continueOnError bool
	concurrentLimit int
}

// Option 是用于配置 Config 的函数类型
type Option func(c *Config)

//// NewRedisShardManager 创建新的 RedisShardManager 实例
//func NewRedisShardManager(redisClient redis.UniversalClient, opts ...Option) *RedisShardManager {
//	config := &Config{
//		batchSize:       defaultBatchSize, // 默认批大小为 50 个 key
//		continueOnError: false,
//		concurrentLimit: defaultConcurrentLimit, // 默认并发限制为 3
//	}
//	for _, opt := range opts {
//		opt(config)
//	}
//	rsm := &RedisShardManager{
//		redisClient: redisClient,
//		config:      config,
//	}
//	return rsm
//}
//
//// WithBatchSize 设置每批处理的 key 数量
//func WithBatchSize(size int) Option {
//	return func(c *Config) {
//		c.batchSize = size
//	}
//}
//
//// WithContinueOnError 设置出错时是否继续处理
//func WithContinueOnError(continueOnError bool) Option {
//	return func(c *Config) {
//		c.continueOnError = continueOnError
//	}
//}
//
//// WithConcurrentLimit 设置并发限制
//func WithConcurrentLimit(limit int) Option {
//	return func(c *Config) {
//		c.concurrentLimit = limit
//	}
//}
//
//// ProcessKeysBySlot 按 Redis 集群哈希槽对 key 分组，并使用传入的函数并发处理。
//func (rsm *RedisShardManager) ProcessKeysBySlot(
//	ctx context.Context,
//	keys []string,
//	processFunc func(ctx context.Context, slot int64, keys []string) error,
//) error {
//
//	// 按槽分组
//	slots, err := groupKeysBySlot(ctx, rsm.redisClient, keys)
//	if err != nil {
//		return err
//	}
//
//	g, ctx := errgroup.WithContext(ctx)
//	g.SetLimit(rsm.config.concurrentLimit)
//
//	// 对每个槽中的 key 使用传入的函数处理
//	for slot, singleSlotKeys := range slots {
//		batches := splitIntoBatches(singleSlotKeys, rsm.config.batchSize)
//		for _, batch := range batches {
//			slot, batch := slot, batch // 避免闭包捕获问题
//			g.Go(func() error {
//				err := processFunc(ctx, slot, batch)
//				if err != nil {
//					log.ZWarn(ctx, "Batch processFunc failed", err, "slot", slot, "keys", batch)
//					if !rsm.config.continueOnError {
//						return err
//					}
//				}
//				return nil
//			})
//		}
//	}
//
//	if err := g.Wait(); err != nil {
//		return err
//	}
//	return nil
//}

// groupKeysBySlot 按 Redis 集群哈希槽（hash slot）对 key 分组。
func groupKeysBySlot(ctx context.Context, redisClient redis.UniversalClient, keys []string) (map[int64][]string, error) {
	slots := make(map[int64][]string)
	clusterClient, isCluster := redisClient.(*redis.ClusterClient)
	if isCluster && len(keys) > 1 {
		pipe := clusterClient.Pipeline()
		cmds := make([]*redis.IntCmd, len(keys))
		for i, key := range keys {
			cmds[i] = pipe.ClusterKeySlot(ctx, key)
		}
		_, err := pipe.Exec(ctx)
		if err != nil {
			return nil, errs.WrapMsg(err, "get slot err")
		}

		for i, cmd := range cmds {
			slot, err := cmd.Result()
			if err != nil {
				log.ZWarn(ctx, "some key get slot err", err, "key", keys[i])
				return nil, errs.WrapMsg(err, "get slot err", "key", keys[i])
			}
			slots[slot] = append(slots[slot], keys[i])
		}
	} else {
		// 非集群客户端时，将所有 key 放入同一个槽（0）
		slots[0] = keys
	}

	return slots, nil
}

// splitIntoBatches 将 key 按指定大小切分为多个批次
func splitIntoBatches(keys []string, batchSize int) [][]string {
	var batches [][]string
	for batchSize < len(keys) {
		keys, batches = keys[batchSize:], append(batches, keys[0:batchSize:batchSize])
	}
	return append(batches, keys)
}

// ProcessKeysBySlot 按 Redis 集群哈希槽对 key 分组，并使用传入的函数并发处理。
func ProcessKeysBySlot(
	ctx context.Context,
	redisClient redis.UniversalClient,
	keys []string,
	processFunc func(ctx context.Context, slot int64, keys []string) error,
	opts ...Option,
) error {

	config := &Config{
		batchSize:       defaultBatchSize,
		continueOnError: false,
		concurrentLimit: defaultConcurrentLimit,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 按槽分组
	slots, err := groupKeysBySlot(ctx, redisClient, keys)
	if err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(config.concurrentLimit)

	// 对每个槽中的 key 使用传入的函数处理
	for slot, singleSlotKeys := range slots {
		batches := splitIntoBatches(singleSlotKeys, config.batchSize)
		for _, batch := range batches {
			slot, batch := slot, batch // 避免闭包捕获问题
			g.Go(func() error {
				err := processFunc(ctx, slot, batch)
				if err != nil {
					log.ZWarn(ctx, "Batch processFunc failed", err, "slot", slot, "keys", batch)
					if !config.continueOnError {
						return err
					}
				}
				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return err
	}
	return nil
}

func DeleteCacheBySlot(ctx context.Context, rcClient *rocksCacheClient, keys []string) error {
	switch len(keys) {
	case 0:
		return nil
	case 1:
		return rcClient.GetClient().TagAsDeletedBatch2(ctx, keys)
	default:
		return ProcessKeysBySlot(ctx, rcClient.GetRedis(), keys, func(ctx context.Context, slot int64, keys []string) error {
			return rcClient.GetClient().TagAsDeletedBatch2(ctx, keys)
		})
	}
}
