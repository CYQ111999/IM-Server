package kafka

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/openimsdk/tools/errs"
)

func CheckTopics(ctx context.Context, conf *Config, topics []string) error {
	kfk, err := BuildConsumerGroupConfig(conf, sarama.OffsetNewest, false)
	if err != nil {
		return err
	}
	cli, err := sarama.NewClient(conf.Addr, kfk)
	if err != nil {
		return errs.WrapMsg(err, "NewClient failed", "config: ", fmt.Sprintf("%+v", conf))
	}
	defer cli.Close()

	existingTopics, err := cli.Topics()
	if err != nil {
		return errs.WrapMsg(err, "Failed to list topics")
	}

	existingTopicsMap := make(map[string]bool)
	for _, t := range existingTopics {
		existingTopicsMap[t] = true
	}

	for _, topic := range topics {
		if !existingTopicsMap[topic] {
			return errs.New("topic not exist", "topic", topic).Wrap()
		}
	}
	return nil
}

func CheckHealth(ctx context.Context, conf *Config) error {
	kfk, err := BuildConsumerGroupConfig(conf, sarama.OffsetNewest, false)
	if err != nil {
		return err
	}
	cli, err := sarama.NewClient(conf.Addr, kfk)
	if err != nil {
		return errs.WrapMsg(err, "NewClient failed", "config: ", fmt.Sprintf("%+v", conf))
	}
	defer cli.Close()

	// 获取 broker 列表
	brokers := cli.Brokers()
	if len(brokers) == 0 {
		return errs.New("no brokers found").Wrap()
	}

	// 检查所有 broker 是否可达
	for _, broker := range brokers {
		if err := broker.Open(kfk); err != nil {
			return errs.WrapMsg(err, "failed to open broker", "broker", broker.Addr())
		}
	}

	return nil
}
