// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kafka

import (
	"context"
	"github.com/IBM/sarama"
	"github.com/openimsdk/tools/errs"
	"google.golang.org/protobuf/proto"
)

// Producer 表示一个 Kafka 生产者。
type Producer struct {
	addr     []string
	topic    string
	config   *sarama.Config
	producer sarama.SyncProducer
}

func NewKafkaProducer(config *sarama.Config, addr []string, topic string) (*Producer, error) {
	producer, err := NewProducer(config, addr)
	if err != nil {
		return nil, err
	}
	return &Producer{
		addr:     addr,
		topic:    topic,
		config:   config,
		producer: producer,
	}, nil
}

// SendMessage 将消息发送到 Producer 中配置的 Kafka topic。
func (p *Producer) SendMessage(ctx context.Context, key string, msg proto.Message) (int32, int64, error) {
	// 序列化 protobuf 消息
	bMsg, err := proto.Marshal(msg)
	if err != nil {
		return 0, 0, errs.WrapMsg(err, "kafka proto Marshal err")
	}
	if len(bMsg) == 0 {
		return 0, 0, errs.WrapMsg(errEmptyMsg, "kafka proto Marshal err")
	}

	// 构造 Kafka 消息
	kMsg := &sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(bMsg),
	}

	// 校验消息的 key 与 value
	if kMsg.Key.Length() == 0 || kMsg.Value.Length() == 0 {
		return 0, 0, errs.Wrap(errEmptyMsg)
	}

	// 将 context 中的元数据附加为消息头
	header, err := GetMQHeaderWithContext(ctx)
	if err != nil {
		return 0, 0, err
	}
	kMsg.Headers = header

	// 发送消息
	partition, offset, err := p.producer.SendMessage(kMsg)
	if err != nil {
		return 0, 0, errs.WrapMsg(err, "p.producer.SendMessage error")
	}

	return partition, offset, nil
}
