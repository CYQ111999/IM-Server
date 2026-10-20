package kafka

import (
	"context"
	"errors"

	"github.com/IBM/sarama"
	"github.com/openimsdk/tools/log"
)

type MConsumerGroup struct {
	sarama.ConsumerGroup
	groupID string
	topics  []string
}

func NewMConsumerGroup(conf *Config, groupID string, topics []string, autoCommitEnable bool) (*MConsumerGroup, error) {
	config, err := BuildConsumerGroupConfig(conf, sarama.OffsetNewest, autoCommitEnable)
	if err != nil {
		return nil, err
	}
	group, err := NewConsumerGroup(config, conf.Addr, groupID)
	if err != nil {
		return nil, err
	}
	return &MConsumerGroup{
		ConsumerGroup: group,
		groupID:       groupID,
		topics:        topics,
	}, nil
}

func (mc *MConsumerGroup) GetContextFromMsg(cMsg *sarama.ConsumerMessage) context.Context {
	return GetContextWithMQHeader(cMsg.Headers)
}

func (mc *MConsumerGroup) RegisterHandleAndConsumer(ctx context.Context, handler sarama.ConsumerGroupHandler) {
	for {
		err := mc.ConsumerGroup.Consume(ctx, mc.topics, handler)
		if errors.Is(err, sarama.ErrClosedConsumerGroup) {
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		if err != nil {
			log.ZWarn(ctx, "consume err", err, "topic", mc.topics, "groupID", mc.groupID)
		}
	}
}

func (mc *MConsumerGroup) Close() error {
	return mc.ConsumerGroup.Close()
}
