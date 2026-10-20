package batcher

import (
	"context"
	"fmt"
	"github.com/openimsdk/tools/utils/stringutil"
	"testing"
	"time"
)

func TestBatcher(t *testing.T) {
	config := Config{
		size:     1000,
		buffer:   10,
		worker:   10,
		interval: 5 * time.Millisecond,
	}

	b := New[string](
		WithSize(config.size),
		WithBuffer(config.buffer),
		WithWorker(config.worker),
		WithInterval(config.interval),
		WithSyncWait(true),
	)

	// 模拟 Do 函数，仅打印数值用于演示
	b.Do = func(ctx context.Context, channelID int, vals *Msg[string]) {
		t.Logf("Channel %d Processed batch: %v", channelID, vals)
	}
	b.OnComplete = func(lastMessage *string, totalCount int) {
		t.Logf("Completed processing with last message: %v, total count: %d", *lastMessage, totalCount)
	}
	b.Sharding = func(key string) int {
		hashCode := stringutil.GetHashCode(key)
		return int(hashCode) % config.worker
	}
	b.Key = func(data *string) string {
		return *data
	}

	err := b.Start()
	if err != nil {
		t.Fatal(err)
	}

	// 测试正常的数据处理流程
	for i := 0; i < 10000; i++ {
		data := "data" + fmt.Sprintf("%d", i)
		if err := b.Put(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(time.Duration(1) * time.Second)
	start := time.Now()
	// 等待所有处理完成
	b.Close()

	elapsed := time.Since(start)
	t.Logf("Close took %s", elapsed)

	if len(b.data) != 0 {
		t.Error("Data channel should be empty after closing")
	}
}
