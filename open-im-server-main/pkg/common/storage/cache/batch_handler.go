package cache

import (
	"context"
)

// BatchDeleter 接口定义了批量删除缓存并发布删除信息的一组方法。
type BatchDeleter interface {
	// ChainExecDel 方法用于链式调用，必须先调用 Clone，以免污染原对象。
	ChainExecDel(ctx context.Context) error
	// ExecDelWithKeys 方法直接按传入的 key 执行删除。
	ExecDelWithKeys(ctx context.Context, keys []string) error
	// Clone 方法返回 BatchDeleter 的副本，避免修改原对象。
	Clone() BatchDeleter
	// AddKeys 方法添加待删除的 key。
	AddKeys(keys ...string)
}
