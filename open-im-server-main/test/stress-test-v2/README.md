# 压力测试 V2

## 使用说明

需要先设置 `TestTargetUserList` 变量。

### 编译

```bash

go build -o test/stress-test-v2/stress-test-v2 test/stress-test-v2/main.go
```

### 运行

```bash

tools/stress-test-v2/stress-test-v2 -c config/
```
