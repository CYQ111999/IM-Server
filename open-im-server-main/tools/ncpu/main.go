package main

import (
	"fmt"
	"runtime"

	"go.uber.org/automaxprocs/maxprocs"
)

func main() {
	// 使用一个不做任何事的自定义 logger 来设置 maxprocs，以忽略日志输出。
	maxprocs.Set(maxprocs.Logger(func(string, ...interface{}) {
		// 故意留空，用于屏蔽 automaxprocs 的全部日志输出。
	}))

	// 这样只会打印 GOMAXPROCS 的值，不会输出 automaxprocs 的日志。
	fmt.Println(runtime.GOMAXPROCS(0))
}
