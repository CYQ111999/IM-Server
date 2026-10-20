# 压力测试

## 使用说明

需要先设置 `TestTargetUserList` 和 `DefaultGroupID` 两个变量。

### 编译

```bash

go build -o test/stress-test/stress-test test/stress-test/main.go
```

### 运行

```bash

tools/stress-test/stress-test -c config/
```
