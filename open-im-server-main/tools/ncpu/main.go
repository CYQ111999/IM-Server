// Copyright © 2024 OpenIM. All rights reserved.
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
