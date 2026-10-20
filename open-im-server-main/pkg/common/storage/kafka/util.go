package kafka

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/mcontext"
)

var errEmptyMsg = errors.New("kafka binary msg is empty")

// GetMQHeaderWithContext 从 context 中提取消息队列头信息。
func GetMQHeaderWithContext(ctx context.Context) ([]sarama.RecordHeader, error) {
	operationID, opUserID, platform, connID, err := mcontext.GetCtxInfos(ctx)
	if err != nil {
		return nil, err
	}
	return []sarama.RecordHeader{
		{Key: []byte(constant.OperationID), Value: []byte(operationID)},
		{Key: []byte(constant.OpUserID), Value: []byte(opUserID)},
		{Key: []byte(constant.OpUserPlatform), Value: []byte(platform)},
		{Key: []byte(constant.ConnID), Value: []byte(connID)},
	}, nil
}

// GetContextWithMQHeader 根据消息队列头信息构造 context。
func GetContextWithMQHeader(header []*sarama.RecordHeader) context.Context {
	var values []string
	for _, recordHeader := range header {
		values = append(values, string(recordHeader.Value))
	}
	return mcontext.WithMustInfoCtx(values) // Attach extracted values to context
}
