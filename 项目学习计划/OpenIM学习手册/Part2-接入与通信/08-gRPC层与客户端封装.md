# 08 gRPC 层与客户端封装：服务之间怎么"互相调用"

> **本篇定位**：前两篇讲的是"客户端 ↔ 服务端"。这一篇讲"**服务端 ↔ 服务端**"——API 怎么调用户服务、消息服务怎么调群组服务。
>
> 这套东西叫 **RPC（远程过程调用）**。理解它，你就理解了微服务的"神经系统"。
>
> **结构**：
> - **第一部分（8.1–8.12）纯理论**：RPC 是什么、为什么不直接用 HTTP、gRPC 三件套、HTTP/2 多路复用、四种通信模式、stub、连接池、负载均衡、拦截器、超时与熔断、错误码、metadata。
> - **第二部分（8.13–8.21）源码**：7 个服务、`msgServer` 精读、`startrpc` 启动器、`rpcli` 封装、错误包装、`incrversion` 算法。
> - **第三部分**：实验。
>
> **本篇名词**：RPC、IDL、gRPC、HTTP/2、多路复用、队头阻塞、Stream、Unary、Stub（存根）、连接池、SubConn、负载均衡、拦截器（Interceptor）、超时、重试、熔断（Circuit Breaker）、gRPC 状态码、metadata、幂等。

---

# 第一部分 理论：远程调用是怎么回事

## 8.1 从"函数调用"到"远程调用"

### 8.1.1 本地调用有多简单

```go
user, err := userService.GetUser(ctx, "user001")
```

这一行在**同一个进程内**执行时，发生的事情是：

```
① 把参数压栈（或放寄存器）
② 跳到函数入口地址
③ 执行
④ 把返回值放回约定位置
⑤ 跳回来
```

**开销：几纳秒到几微秒。** 而且编译器帮你检查类型、IDE 能跳转、出错有堆栈。

### 8.1.2 一旦跨进程，全变了

```go
// 你以为在调用，其实是在发网络请求
user, err := userService.GetUser(ctx, "user001")
```

**实际发生的事**：

```
① 把参数序列化成字节（protobuf）
② 建立/复用一个 TCP 连接
③ 按 HTTP/2 帧格式写出去
④ 等对端响应（网络往返，1~100ms）
⑤ 反序列化响应
⑥ 处理各种失败：超时、连接断开、对端 500、返回了错误码
```

**三件事彻底变了**：

| | 本地调用 | 远程调用 |
|---|---|---|
| 时间量级 | 纳秒~微秒 | **毫秒**（慢 1000 倍以上） |
| 失败模式 | 基本不会失败（除非 panic） | **会超时、会丢、会对端挂了** |
| 语义 | 一定执行或一定不执行 | **可能"不知道执没执行"**（超时） |

**最后一条最要命**：本地调用 `CreateOrder()` 超时是不存在的；远程调用超时后，你**无法确定对方到底创建了没有**。这就是为什么需要**幂等**（第 07 篇的 `ClientMsgID`）。

### 8.1.3 所以 RPC 框架要解决什么

**RPC（Remote Procedure Call）= 让远程调用"看起来像"本地调用。**

要抹平差距，框架必须做五件事：

```
① 序列化/反序列化（把对象变成字节）
② 传输（建立连接、收发字节）
③ 代码生成（让你能写 userService.GetUser 而不是手拼字节）
④ 错误处理（把网络错误、业务错误统一成一种形式）
⑤ 治理（超时、重试、负载均衡、熔断、监控）
```

**① ② 是"能跑"，③④⑤ 是"能上生产"。** 很多手写的"RPC"只做了 ①②，所以一上线就出问题。

---

## 8.2 为什么不直接用 HTTP + JSON 做 RPC

**这其实是个合理的选择**（很多公司早期就这么做）。但对**内部服务间高频调用**，它有三个硬伤：

### 硬伤一：性能

| | HTTP/1.1 + JSON | gRPC + protobuf |
|---|---|---|
| 序列化体积 | 大（字段名重复） | 小（字段号 + varint） |
| 序列化 CPU | 高（文本解析） | 低（二进制跳读） |
| 连接复用 | 需要 keep-alive，仍有队头阻塞 | HTTP/2 多路复用 |
| 头部开销 | 每个请求几百字节 | HPACK 压缩后几字节 |

**一个服务间调用链如果有 10 跳，每跳省 1ms，端到端就省 10ms。** 在高 QPS 下这是巨大的差异。

### 硬伤二：类型安全

```go
// HTTP + JSON：靠文档和约定
resp, _ := http.Get("/user?userID=user001")
var user User
json.Unmarshal(resp.Body, &user)     // ← 字段对不上？运行时才知道
```

```protobuf
// gRPC：靠 IDL，编译期检查
user, err := client.GetUser(ctx, &pb.GetUserReq{UserID: "user001"})   // ← 类型错了编译不过
```

**"编译期发现"和"上线后 500"的区别。**

### 硬伤三：治理能力

HTTP 本身没有"超时传播""重试策略""负载均衡""熔断"的标准做法，每个团队自己造轮子。gRPC 把这些做进了框架（拦截器 + 官方库）。

📌 **但要公允**：**对外 API 用 HTTP+JSON 是对的**（任何语言都能调、可读、好调试）。**内部服务间用 gRPC**。本项目正是这么分的：

```
对外：HTTP（10002）+ WS（10001）
对内：gRPC（各服务端口）
```

---

## 8.3 gRPC 的三件套

gRPC = **IDL（.proto）+ protobuf 序列化 + HTTP/2 传输**。

### 8.3.1 IDL：先写"合同"

```protobuf
// 定义数据结构
message GetUserReq {
  string userID = 1;
}
message GetUserResp {
  UserInfo user = 1;
}

// 定义服务（这是 HTTP 没有的）
service User {
  rpc GetUser(GetUserReq) returns (GetUserResp);
  rpc GetUsersInfo(GetUsersInfoReq) returns (GetUsersInfoResp);
}
```

**`service` 关键字是关键**：它不只定义数据结构，还定义**方法**。编译后生成：

- **服务端**：一个接口 `UserServer`，你实现它；
- **客户端**：一个 `UserClient` 结构体，带 `GetUser` 方法。

**这就是 stub（存根）**——它让远程调用写起来和本地调用一样。

### 8.3.2 代码生成

```bash
protoc --go_out=. --go-grpc_out=. user.proto
```

生成 `user.pb.go`（消息）和 `user_grpc.pb.go`（服务接口 + 客户端）。

**编译期就保证了**：客户端和服务端的参数类型一致。**改 proto 后忘了重新生成，编译就报错**——这是最有效的"接口契约检查"。

### 8.3.3 为什么 HTTP/2 是必须的

**这是很多人忽略的关键点**：gRPC **强依赖 HTTP/2**，不是"可选优化"。

原因见下一节。

---

## 8.4 HTTP/2：解决 HTTP/1.1 的"队头阻塞"

### 8.4.1 HTTP/1.1 的问题

HTTP/1.1 虽然支持 keep-alive（复用 TCP 连接），但**一个连接同一时刻只能处理一个请求**：

```
连接1: [请求A........响应A][请求B........响应B]   ← 串行
```

如果 A 很慢，B 就得排队。这叫 **队头阻塞（Head-of-Line Blocking, HOL）**。

**浏览器的解法**：对同一域名开 6 个并发连接。**服务间的解法**：开连接池（几十个连接）。

**代价**：连接数暴涨、TCP 慢启动反复发生、内存占用高。

### 8.4.2 HTTP/2 的三个改进

**① 二进制分帧（Binary Framing）**

HTTP/1.1 是文本协议，解析要逐字符扫。HTTP/2 把所有内容切成**二进制帧**（HEADERS 帧、DATA 帧），解析快、边界清晰。

**② 多路复用（Multiplexing）** —— 最重要的一个

```
连接1: [帧A1][帧B1][帧A2][帧C1][帧B2][帧A3]...
        ↑ 不同请求的帧交错在同一条连接上
```

每个帧带 **Stream ID**，接收方按 Stream ID 重组。**一条连接上可以同时跑几百个请求**，互不阻塞。

**这就是 gRPC 能"一个连接打天下"的原因**：不需要连接池来并发，一条连接就够了。

**③ HPACK 头部压缩**

HTTP/1.1 每个请求都重复发 `User-Agent`、`Content-Type` 这些头。HTTP/2 维护一张**动态字典**，重复的头只发索引（几个字节）。

⚠️ **注意**：HTTP/2 只解决了**应用层**的队头阻塞。**TCP 层**的队头阻塞还在（一个 TCP 包丢了，后面所有 Stream 都得等重传）。所以才有 HTTP/3（QUIC over UDP）。**这是可以提的深度点**。

### 8.4.3 对本项目的意义

```
7 个 RPC 服务 × 每个服务几十个方法 × 高并发
    ↓
如果用 HTTP/1.1：每个并发调用要一个连接 → 连接数爆炸
    ↓
用 HTTP/2：一个服务一个连接，几百个并发调用跑在上面
```

**这解释了为什么 `rpcli` 里每个服务只持有一个 `ClientConn`**（8.16），而不是连接池。

---

## 8.5 四种通信模式

gRPC 不只支持"一问一答"。

| 模式 | 形态 | 用途 | 本项目用不用 |
|---|---|---|---|
| **Unary（一元）** | 一个请求 → 一个响应 | 绝大多数业务调用 | ✅ 主力 |
| **Server Streaming** | 一个请求 → 一串响应 | 订阅、大数据分批下发 | ⚠️ 少量 |
| **Client Streaming** | 一串请求 → 一个响应 | 上传、聚合上报 | ❌ |
| **Bidi Streaming** | 双向流 | 实时双向通道 | ❌（WS 更适合） |

**为什么 IM 的业务几乎都是 Unary？**

因为"实时推送"这个需求已经被 **WebSocket** 承担了。gRPC 在这里只做"服务间的同步调用"：

```
API → user RPC：查用户信息（Unary）
msg RPC → group RPC：查群成员（Unary）
push → gateway：推消息（Unary，网关内部再写 WS）
```

**职责划分很清楚**：WS 管"服务器到客户端"，gRPC 管"服务器到服务器"。

📌 **面试可能问**："为什么不用 gRPC 流做消息推送？"
答：**可以，但不合适**。① 浏览器不支持 gRPC（需要 grpc-web 代理）；② WS 是浏览器原生能力；③ 长连接的连接管理（心跳、踢人、多端）需要专门做，gRPC 流不提供这些。

---

## 8.6 Stub（存根）：让远程调用"看起来像"本地

### 8.6.1 什么是存根

```go
// 生成代码（简化）
type UserClient interface {
    GetUser(ctx context.Context, in *GetUserReq, opts ...grpc.CallOption) (*GetUserResp, error)
}

type userClient struct {
    cc grpc.ClientConnInterface     // ← 底层连接
}

func (c *userClient) GetUser(ctx context.Context, in *GetUserReq, opts ...grpc.CallOption) (*GetUserResp, error) {
    out := new(GetUserResp)
    err := c.cc.Invoke(ctx, "/openim.user.user/GetUser", in, out, opts...)
    return out, err
}
```

**`cc.Invoke` 就干了所有脏活**：序列化、发帧、等待、反序列化、错误转换。

**你的代码看起来像本地调用**：

```go
resp, err := client.GetUser(ctx, &pb.GetUserReq{UserID: "user001"})
```

**但 `err` 里可能藏着**：网络超时、连接断开、对端 panic、业务错误……**这些本地调用都不会有**。

⚠️ **新手最常见的错误**：把 RPC 调用当本地调用，**不处理 error 或只打日志**。RPC 的 error 是常态，不是异常。

### 8.6.2 服务名和方法名从哪来

```
/openim.user.user/GetUser
 ↑ 包名.服务名    ↑ 方法名
```

**这是 gRPC 的路由键**，等价于 HTTP 的 URL。服务端靠它在注册表里找到实现。

⚠️ **所以改包名 = 破坏兼容**。proto 里的 `package` 一旦上线不能改，否则所有客户端都找不到服务。

---

## 8.7 连接池：gRPC 为什么不太需要

### 8.7.1 一般 RPC 为什么要连接池

```
每个请求建一个 TCP 连接：
  三次握手（1 RTT）+ TLS 握手（2 RTT）→ 几百 ms
  → 完全不可接受
```

所以必须复用连接。而 HTTP/1.1 一个连接只能跑一个请求，所以需要**多个连接组成池**。

### 8.7.2 gRPC 的情况

**HTTP/2 多路复用 → 一条连接能跑几百个并发请求 → 一个连接通常就够。**

```go
// rpcli 里的典型写法
type MsgClient struct {
    msg.MsgClient       // ← 内部持有一个 ClientConn
}
```

**只有一个连接，不是池。**

那什么时候需要多个连接？

| 场景 | 原因 |
|---|---|
| **连接成为瓶颈** | 单连接带宽/流数上限（HTTP/2 默认并发流上限通常 100~250） |
| **多后端负载均衡** | 每个后端一个 SubConn |
| **CPU 核数多** | 单连接的读写 goroutine 可能打不满多核 |

📌 **这是一个"反常识"的知识点**：**gRPC 的默认姿势是"一个 ClientConn 走天下"，不是连接池**。面试时能说清"因为 HTTP/2 多路复用，连接池的意义被削弱了"，说明你懂原理。

---

## 8.8 负载均衡：一个 ClientConn 怎么打到多台机器

### 8.8.1 问题

```
user RPC 部署了 3 个实例：10.0.0.1:20100, 10.0.0.2:20100, 10.0.0.3:20100
API 层只有一个 ClientConn，请求怎么分散？
```

### 8.8.2 gRPC 的解法：SubConn + 解析器 + 负载均衡策略

```
ClientConn
  ├── SubConn 1 → 10.0.0.1:20100
  ├── SubConn 2 → 10.0.0.2:20100
  └── SubConn 3 → 10.0.0.3:20100
```

**工作流程**：

```
① 解析器（Resolver）拿到后端地址列表
   ← 地址从哪来？服务发现（etcd/Consul/DNS）
② 负载均衡器（Balancer）为每个地址建一个 SubConn
③ 每次调用，按策略选一个 SubConn
```

**常用策略**：

| 策略 | 行为 | 优点 | 缺点 |
|---|---|---|---|
| **round_robin** | 轮询 | 均匀 | 不感知后端负载 |
| **pick_first** | 只用第一个可用的 | 简单 | 浪费其他机器 |
| **least_request** | 选当前请求最少的 | 感知负载 | 需要维护计数 |
| **一致性哈希** | 按 key 固定到某台 | 缓存友好 | 负载可能不均 |

📌 **本项目大概率用 round_robin**（gRPC 生态默认），具体看 `rpcli` 建连接时的 `WithDefaultServiceConfig`（📌 待验证）。

### 8.8.3 为什么这很重要

**如果没有负载均衡**，3 个实例里只有 1 个在干活，另外 2 个闲着——**扩容白扩**。

**更糟的是**：如果那 1 个挂了，全部请求失败，而另外 2 个健康却没人用。

---

## 8.9 拦截器：gRPC 的"中间件"

### 8.9.1 概念

**拦截器（Interceptor）= gRPC 版的中间件**。

```go
// 一元拦截器签名
type UnaryServerInterceptor func(
    ctx context.Context,
    req any,
    info *grpc.UnaryServerInfo,
    handler grpc.UnaryHandler,
) (resp any, err error)
```

**你可以在 `handler` 前后插入逻辑**：

```go
func MyInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
    // ① 前置：记日志、验权、限流
    start := time.Now()

    resp, err := handler(ctx, req)      // ② 调用真正的业务方法

    // ③ 后置：统计耗时、转换错误
    log.Info("cost", time.Since(start))
    return resp, err
}
```

**和 HTTP 中间件的区别**：

| | HTTP 中间件 | gRPC 拦截器 |
|---|---|---|
| 作用对象 | `*gin.Context` | `context.Context` + 请求对象 |
| 模式 | 只有 Unary 一种 | **Unary 和 Stream 两套** |
| 注册方式 | `r.Use()` | `grpc.ChainUnaryInterceptor()` |

⚠️ **必须写两套**：如果只注册了 Unary 拦截器，Stream 调用就**完全绕过**所有治理逻辑（日志、鉴权、限流）。**这是 gRPC 的经典坑**。

**本项目的做法是对的**：熔断和限流都注册了两套：

```go
options = append(options,
    UnaryCircuitBreakerInterceptor(breaker),
    StreamCircuitBreakerInterceptor(breaker),
)
```

---

## 8.10 超时、重试、熔断：远程调用的三件保命装备

### 8.10.1 超时（Timeout）

**为什么必须有？**

```
没有超时：user RPC 卡死 → API 的 goroutine 永久阻塞
        → 1000 个请求后 goroutine 耗尽
        → API 也挂了（级联失败）
```

**超时的正确姿势：超时预算（Timeout Budget）传播**

```
客户端设置总超时 3s
  → API 层用掉 0.2s
  → 传给 user RPC 的剩余预算 2.8s
    → user RPC 再调 group RPC，剩 2.6s
```

**这样任何一层都不会"超时了还在干活"**。gRPC 的 `context.WithTimeout` 会通过 metadata 自动传播 deadline。

⚠️ **常见错误**：每层都写死 `context.WithTimeout(ctx, 3*time.Second)`，**覆盖了上游的 deadline**，导致总耗时可能变成 3s × 层数。

**正确做法**：如果上游已有 deadline，不要延长它。

### 8.10.2 重试（Retry）

**不是所有请求都能重试**：

| 请求类型 | 能重试吗 | 原因 |
|---|---|---|
| 查询（幂等） | ✅ | 多查一次无害 |
| 创建（非幂等） | ⚠️ 需要幂等键 | 否则重复创建 |
| 有副作用且无幂等键 | ❌ | 可能重复扣款 |

**重试还会放大故障**：

```
服务 B 变慢（不是挂）
  → 所有客户端超时重试
  → B 收到 2 倍请求
  → B 更慢
  → 重试更多
  → 雪崩
```

**所以重试必须配合**：
- **退避（backoff）**：1s, 2s, 4s... 不要立刻重试；
- **抖动（jitter）**：加随机量，避免所有客户端同时重试；
- **重试预算**：整个调用链最多重试几次（不是每层都重试）；
- **熔断**：见下。

### 8.10.3 熔断（Circuit Breaker）

**核心思想**：**当下游已经明显不行了，就别再打它了，直接快速失败。**

三个状态：

```
        失败率超阈值
CLOSED ──────────────→ OPEN
  ↑                      │
  │  试探成功             │ 等待一段时间
  │                      ↓
  └──────────────── HALF_OPEN
                     （放少量请求试探）
```

| 状态 | 行为 |
|---|---|
| **CLOSED** | 正常放行，统计失败率 |
| **OPEN** | **全部拒绝**，不请求下游（快速失败） |
| **HALF_OPEN** | 放少量请求试探，成功则回 CLOSED，失败则回 OPEN |

**为什么这有用？**

```
下游挂了
  → 熔断打开
  → 上游立刻返回错误（不再等 3s 超时）
  → 上游的 goroutine 不被占用
  → 上游还能服务其他请求
  → 下游恢复后自动探测恢复
```

**没有熔断**：上游所有 goroutine 都卡在等下游超时 → 上游也挂 → 级联雪崩。

📌 **熔断 vs 限流的区别**（容易混）：

| | 限流 | 熔断 |
|---|---|---|
| 保护谁 | **保护自己**（不被流量压垮） | **保护自己不被下游拖死** |
| 触发条件 | 请求量/CPU 高 | **下游失败率高** |
| 方向 | 入口 | 出口 |

---

## 8.11 gRPC 错误码：和 HTTP 状态码不同

gRPC 有自己的一套状态码（`codes.Code`）：

| Code | 数值 | 含义 | 类比 HTTP |
|---|---|---|---|
| `OK` | 0 | 成功 | 200 |
| `Canceled` | 1 | 被取消 | 499 |
| `Unknown` | 2 | 未知错误 | 500 |
| `InvalidArgument` | 3 | 参数错误 | 400 |
| `DeadlineExceeded` | 4 | **超时** | 504 |
| `NotFound` | 5 | 不存在 | 404 |
| `AlreadyExists` | 6 | 已存在 | 409 |
| `PermissionDenied` | 7 | 无权限 | 403 |
| `ResourceExhausted` | 8 | **资源耗尽（限流）** | 429 |
| `FailedPrecondition` | 9 | 状态不对 | 400 |
| `Aborted` | 10 | 中止（并发冲突） | 409 |
| `Unimplemented` | 12 | 未实现 | 501 |
| `Internal` | 13 | 内部错误 | 500 |
| `Unavailable` | 14 | **不可用（熔断/连接失败）** | 503 |
| `Unauthenticated` | 16 | 未认证 | 401 |

### 8.11.1 关键区分：哪些错误可以重试

| Code | 能重试吗 |
|---|---|
| `Unavailable` | ✅ 通常是瞬时的 |
| `DeadlineExceeded` | ⚠️ 谨慎（可能已经执行了） |
| `ResourceExhausted` | ✅ 退避后重试 |
| `InvalidArgument` | ❌ 重试多少次都一样 |
| `NotFound` | ❌ |
| `Internal` | ⚠️ 可能是 bug |

**重试策略必须按 code 区分**，不能"所有错误都重试"。

### 8.11.2 本项目怎么用

```go
errs.NewCodeError(2003, "args error")    // 自定义业务码
errs.ErrInternalServer.WrapMsg("...")    // 预定义错误
```

**注意：本项目用的是"业务错误码"而不是标准 gRPC code。**

```go
// 内部实现大致是
status.Error(codes.Code(errCode), errMsg)
```

**这意味着**：gRPC 框架层面的重试策略（按 code 判断）会**失效**，因为所有错误都是 `Unknown` 或自定义 code。

📌 **这是一个设计取舍**：
- **好处**：业务码更精细（能区分"群不存在"和"用户不存在"）；
- **代价**：失去标准 code 的通用语义（负载均衡器、服务网格看不懂）。

**更成熟的做法**：**两层错误**——标准 code 表达"类别"，details 里放业务码。

---

## 8.12 metadata：gRPC 的"请求头"

### 8.12.1 是什么

**metadata 就是 gRPC 的 HTTP header**（底层真的是 HTTP/2 的 HEADERS 帧）。

```go
// 客户端发
ctx = metadata.AppendToOutgoingContext(ctx, "operationID", "op-abc")

// 服务端收
md, ok := metadata.FromIncomingContext(ctx)
opID := md.Get("operationID")
```

### 8.12.2 用来传什么

| 内容 | 例子 |
|---|---|
| **链路追踪 ID** | `operationID` |
| **调用者身份** | `userID`（内部服务间传递） |
| **超时 deadline** | 自动通过 `grpc-timeout` 头传 |
| **压缩算法** | `grpc-encoding` |
| **自定义路由标签** | 灰度发布用 |

⚠️ **metadata 的 key 会被转成小写**（HTTP/2 要求）。所以 `operationID` 存进去，取出来是 `operationid`。**用 `Get()` 时不用管大小写，它内部会处理**。

### 8.12.3 安全警告

⚠️ **metadata 可以被客户端伪造**。

如果有人能直连你的 gRPC 端口，他可以随便塞 `userID: admin` 进 metadata。**所以**：

1. **gRPC 端口绝不能暴露公网**（这是 05 篇强调过的红线）；
2. **下游不能无条件信任 metadata 里的身份**——最好由网关/入口服务用内部密钥签名。

📌 这就是 **mTLS（双向 TLS）** 存在的意义：不仅加密，还**证明"调用者确实是某个服务"**。

---

## 8.13 理论部分小结

```
远程调用 = 序列化 + 传输 + 代码生成 + 错误处理 + 治理
    ↓
比本地调用慢 1000 倍、会失败、可能"不知道执没执行"（→ 需要幂等）
    ↓
HTTP+JSON 能做，但内部高频调用有三个硬伤：性能、类型安全、治理
    ↓
gRPC = IDL(.proto) + protobuf + HTTP/2
    ↓
HTTP/2 多路复用 → 一条连接跑几百个并发请求 → 不需要连接池
    ↓ 但 TCP 层队头阻塞还在（→ HTTP/3）
四种模式：Unary 最常用；实时推送交给 WebSocket，不用 gRPC 流
    ↓
Stub 让远程调用写起来像本地，但 error 是常态，必须处理
    ↓
多实例靠 SubConn + 解析器 + 负载均衡策略（round_robin 等）
拦截器 = gRPC 中间件，但 Unary 和 Stream 要注册两套（经典坑）
    ↓
三件保命装备：超时（预算传播）、重试（退避+抖动+幂等）、熔断（快速失败）
gRPC 错误码和 HTTP 不同；本项目用自定义业务码，牺牲了标准语义
metadata 是 gRPC 的 header，可传 traceID，但可被伪造 → 端口不能暴露
```

**第二部分开始，看这 7 个服务怎么组织。**

---

# 第二部分 源码：7 个服务怎么组织

## 8.14 七个服务一览

⚠️ **先纠正一个常见误解**：`internal/rpc` 下**只有一个 `server.go`**（在 `msg/` 下）。其他六个服务的结构体和 `Start` 都写在同名文件里。**不要去找 `user/server.go`，它不存在。**

| 服务 | 结构体 | 定义 | Start | 注册的 gRPC 服务 | 主要依赖 |
|---|---|---|---|---|---|
| auth | `authServer` | `auth/auth.go:34-42` | `auth.go:53` | `openim.auth.Auth` | **只有 Redis**（Mongo 配置声明了但没用） |
| user | `userServer` | `user/user.go:43-57` | `user.go:71` | `openim.user.user` | Mongo, Redis, group/relation/msg 客户端 |
| relation | `friendServer` | `relation/friend.go:31-41` | `friend.go:55` | `openim.relation.friend` | Mongo, Redis, user/msg 客户端, `memamq(16,1MB)` |
| group | `groupServer` | `group/group.go:46-56` | `group.go:69` | `openim.group.group` | Mongo, Redis, user/msg/conversation 客户端 |
| msg | `msgServer` | `msg/server.go:44-60` | `server.go:67` | `openim.msg.msg` | Mongo, Redis, Kafka Producer, 4 个 rpccache, 4 个客户端 |
| conversation | `conversationServer` | `conversation/conversation.go:34-45` | `conversation.go:58` | `openim.conversation.conversation` | Mongo, Redis, user/group/msg 客户端 |
| third | `thirdServer` | `third/third.go:31-39` | `third.go:52` | `openim.third.third` | Mongo, Redis, 对象存储 |

### 注册点（真正调用 `RegisterXxxServer` 的行）

```
auth/auth.go:71              pbauth.RegisterAuthServer(server, &authServer{...})
user/user.go:128             pbuser.RegisterUserServer(server, u)
relation/friend.go:106       relation.RegisterFriendServer(server, &friendServer{...})
group/group.go:114           pbgroup.RegisterGroupServer(server, &gs)
msg/server.go:134            msg.RegisterMsgServer(server, s)
conversation/conversation.go:102  pbconversation.RegisterConversationServer(server, &cs)
third/third.go:99            third.RegisterThirdServer(server, &thirdServer{...})
```

**这 7 行就是"服务注册"的全部**。对照理论 8.6.2：`RegisterXxxServer` 把实现挂到 gRPC 的**方法注册表**上，之后收到 `/openim.user.user/GetUser` 就能路由到 `u.GetUser`。

⚠️ **注意 `incrversion` 不在这个表里**。它是一个**算法包**，不是服务——这是下一节的重点纠正。

### 所有服务的 Config 形状一致

```go
type Config struct {
    RpcConfig        conf.LocalConfig
    RedisConfig      conf.Redis
    MongodbConfig    conf.Mongo
    KafkaConfig      conf.Kafka
    Notification     conf.Notification
    Share            conf.Share
    WebhooksConfig   conf.Webhooks
    LocalCacheConfig conf.LocalCache
    Discovery        conf.Discovery
    Index            conf.Index
}
```

**为什么 7 个服务用同一个形状？**

因为 `startrpc.Start` 是**泛型函数**，内部靠**反射**从 `config` 里按类型找子配置（8.16.4）。**字段名和类型对齐，反射才能找到。**

📌 **这是"约定优于配置"的一个隐式约定**：新增服务时，Config 必须包含这些字段，否则反射取不到，功能静默失效（比如限流不生效、admin 名单为空）。**这类隐式约定是维护风险**——没有编译期检查。

---

## 8.15 精读 `msgServer`：依赖最多的服务

```go
// msg/server.go:44-60
type msgServer struct {
    Database         controller.CommonDB
    MsgDatabase      controller.MsgDatabase
    Redis            redis.RedisClient
    MsgModel         controller.MsgDatabase
    Producer         *kafka.Producer
    WebhookClient    *webhook.Client
    userClient       *rpcli.UserClient
    groupClient      *rpcli.GroupClient
    conversationClient *rpcli.ConversationClient
    relationClient   *rpcli.RelationClient
    msgNotification  ...
    // 4 个 rpccache
}
```

**依赖分四类**：

| 类型 | 字段 | 作用 |
|---|---|---|
| **存储** | `Database`、`MsgDatabase` | Mongo（通用 + 消息专用） |
| **缓存** | `Redis` | seq 分配、消息缓存 |
| **MQ** | `Producer` | 投递 `toRedis` topic |
| **其他服务** | 4 个 `*rpcli.XxxClient` | 查用户、查群、更新会话、查关系 |
| **Webhook** | `WebhookClient` | 回调业务方 |

📌 **这是整个系统里依赖最多的结构体**。原因很自然：发一条消息要
- 查发送者信息（user）
- 校验群成员（group）
- 校验好友关系（relation）
- 更新会话（conversation）
- 写 Mongo、写 Redis、投 Kafka

**依赖多 = 扇出大 = 故障面大**。所以 msg 服务是最需要监控的服务。

### 依赖注入发生在 `Start`

```go
// msg/server.go:67 起（简化）
func Start(ctx context.Context, config *Config, client discovery.Conn, server grpc.ServiceRegistrar) error {
    // ① 连 Mongo
    // ② 建 Redis
    // ③ 建 Kafka Producer
    // ④ 用 discovery 的 client 建 4 个 rpcli
    s := &msgServer{...}
    msg.RegisterMsgServer(server, s)    // :134
    return nil
}
```

**注意顺序**：**先建依赖，再注册**。如果注册后再建依赖，中间可能收到请求但依赖是 nil → panic。

⚠️ **更严谨的做法**：注册前把 `ready` 标志置位，或者用健康检查。本项目没有显式的 ready 门控（📌 待验证是否靠启动顺序保证）。

---

## 8.16 `startrpc.Start`：一个泛型启动器

### 8.16.1 签名（很长，但每一段都有用）

```go
// pkg/common/startrpc/start.go:36
func Start[T any](
    ctx context.Context,
    disc *conf.Discovery,
    circuitBreakerConfig *conf.CircuitBreaker,
    rateLimiterConfig *conf.RateLimiter,
    prometheusConfig *conf.Prometheus,
    listenIP, registerIP string,
    autoSetPorts bool,
    rpcPorts []int,
    index int,
    rpcRegisterName string,
    notification *conf.Notification,
    config T,                                    // ← 泛型配置
    watchConfigNames []string,
    watchServiceNames []string,
    rpcFn func(ctx, config T, client discovery.SvcDiscoveryRegistry, server grpc.ServiceRegistrar) error,
    options ...grpc.ServerOption,
) error
```

**按功能拆解这 14 个参数**：

| 分组 | 参数 | 作用 |
|---|---|---|
| **发现** | `disc`、`rpcRegisterName`、`registerIP`、`listenIP` | 注册到 etcd |
| **端口** | `rpcPorts`、`index`、`autoSetPorts` | 多实例端口分配 |
| **治理** | `circuitBreakerConfig`、`rateLimiterConfig` | 熔断、限流 |
| **监控** | `prometheusConfig` | 指标 |
| **配置** | `config T`、`watchConfigNames` | 泛型配置 + 热更新 |
| **依赖** | `watchServiceNames` | 监听其他服务变化 |
| **回调** | `rpcFn` | **每个服务自己的初始化逻辑** |
| **扩展** | `options` | 额外的 gRPC server 选项 |

**核心设计：模板方法模式**

```
startrpc.Start 负责"所有服务都一样的事"：
    建 gRPC server、装拦截器、连 etcd、注册、监听退出

rpcFn 负责"每个服务不一样的事"：
    连自己的数据库、建自己的依赖、注册自己的 gRPC 服务
```

**收益**：7 个服务不用各写一遍启动逻辑。**改一处（比如加个新拦截器），7 个服务全部生效。**

📌 **这是"框架代码"和"业务代码"分离的标准做法**。看 `cmd/*/main.go`，每个只有几十行，因为脏活都在 `startrpc` 里。

### 8.16.2 五个内置拦截器

```go
options = append(options,
    grpcsrv.GrpcServerMetadataContext(),    // ①
    grpcsrv.GrpcServerErrorConvert(),       // ②
    grpcsrv.GrpcServerLogger(),             // ③
    grpcsrv.GrpcServerRequestValidate(),    // ④
    grpcsrv.GrpcServerPanicCapture(),       // ⑤
)
```

| # | 拦截器 | 作用 | 对应理论 |
|---|---|---|---|
| ① | MetadataContext | 把 metadata 里的 operationID 等注入 ctx | 8.12 |
| ② | ErrorConvert | 把内部 error 转成 gRPC status | 8.11 |
| ③ | Logger | 记录每次调用的方法、耗时、错误 | 8.9 |
| ④ | RequestValidate | 校验请求（用 `validate` tag） | 05 篇 5.8 |
| ⑤ | PanicCapture | 捕获 panic，转成 Internal 错误 | — |

⚠️ **⑤ 极其重要**：**一个服务里的 panic 不应该让整个进程崩溃**。gRPC 默认不会捕获 panic，一个空指针就能让整个 RPC 服务挂掉。

**顺序也有讲究**：`PanicCapture` 应该在最外层（最先注册），这样能捕获后面所有拦截器和业务代码的 panic。

### 8.16.3 熔断与限流的注册

```go
if circuitBreakerConfig.Enable {
    breaker := NewCircuitBreaker(cb)
    options = append(options,
        UnaryCircuitBreakerInterceptor(breaker),
        StreamCircuitBreakerInterceptor(breaker),    // ← 两套
    )
}

if rateLimiterConfig.Enable {
    limiter := NewRateLimiter(...)
    options = append(options,
        UnaryRateLimitInterceptor(limiter),
        StreamRateLimitInterceptor(limiter),         // ← 两套
    )
}
```

**这正好印证了理论 8.9 的"经典坑"**：**必须注册 Unary 和 Stream 两套**。本项目做对了。

📌 **你可以用这个点考察别人**："你们的 gRPC 限流拦截器注册了几套？" 只注册 Unary 的人，Stream 接口就是裸奔的。

### 8.16.4 反射取配置：一个隐式约定

```go
maxRequestBody := getConfigRpcMaxRequestBody(reflect.ValueOf(config))
shareConfig := getConfigShare(reflect.ValueOf(config))
```

**它在干什么？** 从泛型 `config T` 里，按**字段名/类型**找到 `RpcConfig` 和 `Share`。

```go
// 大致实现（简化）
func getConfigShare(v reflect.Value) *conf.Share {
    for i := 0; i < v.NumField(); i++ {
        if f, ok := v.Field(i).Interface().(conf.Share); ok {
            return &f
        }
    }
    return nil
}
```

**为什么要这样？** 因为 `Start[T any]` 不知道 `T` 具体是什么类型（7 个服务的 Config 是不同的 struct）。**只能用反射按类型找。**

**收益**：一个启动器服务 7 个不同的 Config 类型。

**代价（要清楚）**：
- **没有编译期检查**：如果某个服务的 Config 忘了加 `Share` 字段，`shareConfig` 返回 nil，**admin 功能静默失效**；
- **性能**：反射比直接访问慢（但只在启动时执行一次，可忽略）；
- **可读性**：看代码看不出"必须有 Share 字段"这个约束。

📌 **更好的设计**：定义一个接口 `type Config interface { GetShare() *conf.Share; GetRPC() *conf.LocalConfig }`，让 7 个 Config 都实现它。**用接口代替反射，把运行时错误变成编译期错误**。

**这是一个很值得在面试里提的改进点**。

### 8.16.5 请求体大小限制

```go
if maxRequestBody.RequestMaxBodySize > 0 {
    options = append(options, grpc.MaxRecvMsgSize(maxRequestBody.RequestMaxBodySize))
    clientOptions = append(clientOptions, grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(...)))
}
```

**gRPC 默认最大消息 4MB**（收发都是）。**超过就报 `ResourceExhausted`**。

**为什么要限制？**
- 防止单个大请求占满内存；
- 防止恶意构造超大消息打挂服务。

**本项目做成可配置**——因为有些接口确实需要传大 payload（比如批量拉取大量消息）。

⚠️ **注意收发两个方向都要配**：
- `MaxRecvMsgSize`（服务端收）
- `MaxSendMsgSize`（服务端发）
- `MaxCallSendMsgSize`（客户端发）
- `MaxCallRecvMsgSize`（客户端收）

**只配一边会怎样？** 客户端能发出去，服务端收不了 → 报错信息是"received message larger than max"，但你会以为是服务端配置问题，实际是客户端没配。

📌 **这是个很容易排查半天的坑**，记住四个都要配。

---

## 8.17 `rpcli`：客户端封装为什么这么薄

### 8.17.1 结构

```go
// pkg/rpcli/msg.go
func NewMsgClient(cc grpc.ClientConnInterface) *MsgClient {
    return &MsgClient{msg.NewMsgClient(cc)}
}

type MsgClient struct {
    msg.MsgClient        // ← 嵌入官方生成的客户端
}
```

**两个动作**：
1. **嵌入（embed）** 官方 client → 自动获得所有 RPC 方法；
2. **包装一个构造函数** → 统一入口。

**就这么简单。** 为什么不写更多？

| 想做的事 | 为什么不做 |
|---|---|
| 加连接池 | HTTP/2 不需要（理论 8.7） |
| 加重试 | 由拦截器统一做，不该在每个 client 里做 |
| 加超时 | 由 ctx 控制（调用方决定） |
| 加日志 | 拦截器做了 |

📌 **"薄"是刻意的**：**治理逻辑放拦截器，客户端只做类型封装。** 如果每个 client 都塞重试逻辑，就重复了 7 遍，而且行为不一致。

### 8.17.2 但它不完全是空的：便捷方法

```go
func (x *MsgClient) GetMaxSeqs(ctx context.Context, conversationIDs []string) (map[string]int64, error) {
    if len(conversationIDs) == 0 {
        return nil, nil                      // ← 空入参短路
    }
    req := &msg.GetMaxSeqsReq{ConversationIDs: conversationIDs}
    return extractField(ctx, x.MsgClient.GetMaxSeqs, req, (*msg.SeqsInfoResp).GetMaxSeqs)
}
```

**这个方法和 gRPC 原生方法的区别**：

| | 原生 | 包装后 |
|---|---|---|
| 入参 | `*GetMaxSeqsReq` | `[]string` |
| 出参 | `*SeqsInfoResp` | `map[string]int64` |
| 空值处理 | 调用方自己做 | 内部短路 |

**收益**：调用方少写样板代码，且**空入参不会打网络**（`len == 0` 直接返回）。

**`extractField` 是个泛型工具**：调 RPC → 检查错误 → 从响应里抽字段。

```go
func extractField[Req, Resp, Out any](
    ctx context.Context,
    fn func(context.Context, *Req, ...grpc.CallOption) (*Resp, error),
    req *Req,
    get func(*Resp) Out,
) (Out, error) {
    resp, err := fn(ctx, req)
    if err != nil {
        var zero Out
        return zero, err
    }
    return get(resp), nil
}
```

**这是泛型的好用法**：一份代码适配所有"调 RPC 再抽字段"的场景。

📌 **面试可以提**："客户端封装用泛型工具把'调用+错误检查+抽字段'抽出来，避免每个方法重复三行样板。"

### 8.17.3 `tool.go` 里还有什么

`rpcli/tool.go`（从命名推测）放的是 `extractField`、`ignoreResp` 这些泛型辅助函数。

```go
func ignoreResp[T any](resp T, err error) error { return err }
```

**一行代码，但解决了"只关心 error 不关心响应"的场景**，避免写 `_, err := ...`。

---

## 8.18 错误包装：`errs` 包的设计

### 8.18.1 三层结构

```
① 底层：github.com/openimsdk/tools/errs
        提供 Wrap / WrapMsg / NewCodeError

② 项目层：pkg/common/servererrs
        预定义所有业务错误（ErrArgs、ErrTokenNotValid...）

③ 使用层：业务代码
        return servererrs.ErrGroupNotFound.WrapMsg("groupID: " + gid)
```

### 8.18.2 `WrapMsg` 的语义

```go
servererrs.ErrArgs.WrapMsg("header must have token")
//           ↑ 错误码来源              ↑ 附加细节
```

**结果**：
- `errCode` **保留** 原错误的码（2003）；
- `errMsg` **替换/补充** 为 "header must have token"。

**为什么这样设计？**

因为错误码要**稳定**（客户端靠它判断），而错误信息要**具体**（给人看、帮排查）。

```json
{"errCode":2003,"errMsg":"header must have token"}     ← 好
{"errCode":2003,"errMsg":"args error"}                 ← 差（不知道哪个参数）
```

### 8.18.3 错误怎么跨 gRPC 传

```
服务端：return errs.NewCodeError(2003, "...")
    ↓ 被 GrpcServerErrorConvert 拦截器转成 status.Error
客户端：收到 error
    ↓ 被客户端拦截器还原成 *errs.Error
上层：errs.Unwrap(err) 拿到 errCode
```

**为什么要转换两次？**

因为 **gRPC 的 error 只能传 `codes.Code`（整数）+ message（字符串）**，无法直接传 Go 的结构体。所以要：
1. 服务端把 `*errs.Error` 编码进 status message（通常是 JSON）；
2. 客户端解出来还原。

📌 **这是所有 RPC 框架都要解决的问题**：**错误信息跨进程传输时，会丢失类型信息**。要么编码成字符串再解，要么用 `status.Details`（protobuf Any）。

⚠️ **一个隐患**：如果转换/还原不对称，就会**丢失错误码**，客户端只能看到 `Unknown`。**排查这类问题的关键是看两端的拦截器是否配对**。

---

## 8.19 `incrversion`：一个被误解的"服务"

### 8.19.1 先纠正误解

很多人看目录名 `internal/rpc/incrversion`，以为它是第 8 个 RPC 服务。

**它不是。** 证据：
- 目录下只有 `option.go`、`batch_option.go`；
- **没有 `Start` 函数**；
- **没有 `RegisterXxxServer`**；
- **没有 `cmd/` 入口**。

**它是一个泛型增量同步算法包**，被 relation/group/conversation 的 `sync.go` 调用。

📌 **这个误解很典型**：**目录位置 ≠ 架构角色**。判断一个包是不是服务，看三样东西：`Start`、`Register`、`cmd/` 入口。

### 8.19.2 它解决什么问题：增量同步

**场景**：客户端要同步好友列表/群成员/会话列表。

**笨办法**：每次都拉全量。

```
10 万好友 → 每次拉 10 万条 → 带宽爆炸、客户端卡死
```

**改进**：只拉"变化的部分"。

```
客户端：我本地版本是 v100
服务端：v100 之后有 3 条新增、2 条更新、1 条删除 → 给你这 6 条
```

**这就是增量同步**。需要一个"版本号"机制。

### 8.19.3 两个核心概念

**① VersionLog（版本日志）**

每次数据变更，记一条日志：

```
{ id: "user001", version: 101, type: "insert" }
{ id: "user002", version: 102, type: "update" }
{ id: "user003", version: 103, type: "delete" }
```

**② VersionNumber（当前版本）**

每个实体维护一个当前版本号，客户端保存"我看到的最新版本"。

### 8.19.4 算法流程

```
输入：客户端版本 VersionNumbers[i]
① 查 VersionLog：找 version > 客户端版本 的所有变更
② 分类：insertList / updateList / deleteIds
③ 对 insert/update 的 id，去数据库查完整数据（Find）
④ 如果变更太多（超过阈值）→ 返回 fullMap=true，让客户端拉全量
⑤ 组装成响应
```

**第 ④ 步是关键优化**：如果客户端版本太旧（比如离线一个月），增量可能有几万条，**不如直接拉全量**。这叫**降级为全量同步**。

### 8.19.5 泛型设计

```go
type BatchOption[A, B any] struct {
    Ctx            context.Context
    TargetKeys     []string
    VersionIDs     []string
    VersionNumbers []uint64

    Versions        func(ctx, dIds []string, versions []uint64, limits []int) (map[string]*model.VersionLog, error)
    CacheMaxVersions func(ctx, dIds []string) (map[string]*model.VersionLog, error)
    Find            func(ctx, dId string, ids []string) (A, error)
    Resp            func(versionsMap, deleteIdsMap, insertListMap, updateListMap, fullMap) *B
}
```

**设计精髓：把"变化的部分"做成函数参数（策略模式）**

| 参数 | 为什么是函数 |
|---|---|
| `Find` | 不同业务查不同的表（好友表/群表/会话表） |
| `Versions` | 不同业务的版本日志结构不同 |
| `Resp` | 不同业务的响应结构不同 |

**不变的流程（查版本、分类、组装）写在算法里；变化的部分由调用方注入。**

📌 **这是"泛型 + 策略模式"的教科书用法**。`A` 是业务实体类型，`B` 是响应类型。

### 8.19.6 `BatchOption` 无人调用？

记忆里有一条：**`BatchOption` 定义了但没人调用**。

**如果属实，说明什么？**
- 可能是重构到一半的产物；
- 或者是为未来优化预留的接口。

⚠️ **这需要你自己验证**：`grep -rn "BatchOption" --include="*.go" .`，看除了定义处还有没有使用。

📌 **发现"写好了但没用"的代码，是读源码的重要收获**——它说明项目在演进中，不是"完美设计一次成型"。

---

## 8.20 熔断与限流：RPC 层的实现

### 8.20.1 熔断器

```go
// startrpc/circuitbreaker.go
cb := &CircuitBreaker{
    Enable:  circuitBreakerConfig.Enable,
    Success: circuitBreakerConfig.Success,     // 成功率阈值
    Request: circuitBreakerConfig.Request,     // 最少请求数
    Bucket:  circuitBreakerConfig.Bucket,      // 窗口分桶
    Window:  circuitBreakerConfig.Window,      // 窗口时长
}
breaker := NewCircuitBreaker(cb)
```

**四个参数的配合**（对照理论 8.10.3）：

```
在 Window 时间内，分 Bucket 个桶统计
   ↓
总请求数 >= Request（样本足够才有意义）
   ↓
成功率 < Success
   ↓
熔断打开
```

**为什么需要 `Request` 这个"最少请求数"？**

因为**样本太少时，统计不可靠**：
```
1 个请求失败 → 成功率 0% → 熔断？
```

**这是"小样本误判"**。所以要求"至少 N 个请求"才触发熔断。**这是熔断器实现的标配细节**，能说出来说明你懂实现。

### 8.20.2 限流器

```go
limiter := NewRateLimiter((*RateLimiter)(rateLimiterConfig))
```

**参数和 API 层的 BBR 一样**（Window/Bucket/CPUThreshold），因为用的是同一个库 `stability/ratelimit/bbr`。

**区别**：
- API 层限流在 HTTP 中间件；
- RPC 层限流在 gRPC 拦截器。

**两层都限流的理由**：
- API 层保护 HTTP 入口；
- RPC 层保护**每个具体服务**（因为服务可能被其他服务直接调用，不经过 API）。

📌 **这是"纵深防御"（Defense in Depth）**：不指望一个地方挡住所有流量。

---

## 8.21 设计评价

### 做得好的四处

**① `startrpc` 的模板方法**

7 个服务共享一套启动逻辑，改一处全局生效。**框架代码和业务代码彻底分离。**

**② 内置 5 个拦截器，覆盖了基本治理**

日志、错误转换、参数校验、panic 捕获、上下文注入——**新服务什么都不用配就具备这些能力**。

**③ 熔断和限流都注册了 Unary + Stream 两套**

避开了"只注册 Unary 导致 Stream 裸奔"的经典坑。

**④ `rpcli` 保持"薄"**

治理逻辑不重复写在 7 个 client 里。**这是克制**——很多人会忍不住在每个 client 里加重试。

### 有问题的三处

**① 反射取配置，没有编译期检查**（`startrpc/start.go`）

`getConfigShare(reflect.ValueOf(config))` 找不到就返回 nil，功能静默失效。

**改法**：定义接口，要求 7 个 Config 实现：
```go
type Config interface {
    GetShare() *conf.Share
    GetRPCConfig() *conf.LocalConfig
}
```

**② 错误码绕过了标准 gRPC code**

所有业务错误都是自定义码，**gRPC 生态的标准治理失效**（负载均衡器、服务网格按 code 判断重试/熔断都不准）。

**改法**：标准 code 表达类别，业务码放 `status.Details`。

**③ `incrversion` 的 `BatchOption` 疑似死代码**

定义了泛型批处理选项但没人用。**要么删，要么补文档说明是预留**——留着不动会让后来者困惑。

📌 **这条要验证后再写进简历**。别把"我没搜到"当成"确实没有"。

---

# 第三部分 实验

## 实验 1：用 grpcurl 直接调一个 RPC

```bash
# 需要先反射开启（或提供 proto 文件）
grpcurl -plaintext 127.0.0.1:20100 list
```

**预期**：列出该服务注册的所有 gRPC 服务和方法。

⚠️ **如果报 "server does not support the reflection API"**，说明项目**没开 gRPC 反射**。这是常见的安全默认（不暴露接口清单）。那就只能用 `.proto` 文件：

```bash
grpcurl -plaintext -proto user.proto \
  -d '{"userID":"user001"}' \
  127.0.0.1:20100 openim.user.user/GetUser
```

**这个实验能让你直观看到"RPC 调用就是一次带方法名的请求"。**

## 实验 2：观察拦截器加了什么头

用 `grpcurl -v` 或抓包看 gRPC 请求的 HEADERS 帧：

```
grpc-timeout: 2999m
operationid: op-abc
content-type: application/grpc
grpc-accept-encoding: gzip
```

**预期**：能看到 `operationid`（metadata）和 `grpc-timeout`（deadline 传播）。

**这验证了理论 8.12：metadata 就是 HTTP/2 的 header。**

## 实验 3：验证消息大小限制

```bash
# 发一个超过 4MB 的请求
grpcurl -plaintext -d @ 127.0.0.1:20100 openim.msg.msg/SendMsg < big.json
```

**预期**：`ResourceExhausted: grpc: received message larger than max`。

**这验证了 8.16.5 的四个大小限制参数**。

## 实验 4：验证 Unary 和 Stream 拦截器的差异

这个需要写代码：注册一个只加了 Unary 拦截器的服务，然后调一个 Stream 方法——你会发现**日志/限流全部失效**。

**这是理解"为什么必须注册两套"的最直接方式。**

---

# 第四部分 自测题

## 理论层

1. 本地调用和远程调用，哪三件事变了？
2. 为什么远程调用超时后"不知道对方执没执行"？这要求什么设计？
3. RPC 框架必须解决的五个问题是什么？
4. HTTP+JSON 做内部 RPC 的三个硬伤？
5. gRPC 的三件套是什么？
6. 为什么 gRPC 强依赖 HTTP/2？
7. HTTP/1.1 的队头阻塞是什么？HTTP/2 怎么解决？TCP 层的队头阻塞解决了吗？
8. HTTP/2 多路复用靠什么标识不同的请求？
9. 四种通信模式？为什么 IM 的推送不用 gRPC 流？
10. Stub 帮你做了什么？为什么说"RPC 的 error 是常态"？
11. gRPC 为什么不太需要连接池？
12. SubConn 是什么？负载均衡的三种策略各有什么问题？
13. 拦截器为什么必须注册 Unary 和 Stream 两套？
14. 超时预算传播是什么？每层都写死 3s 有什么问题？
15. 重试为什么会放大故障？四个配套措施是什么？
16. 熔断的三个状态？为什么需要"最少请求数"？
17. 熔断和限流的区别（保护谁、触发条件）？
18. gRPC 哪些错误码可以重试？哪些绝对不能？
19. metadata 是什么？为什么它可被伪造？防线是什么？
20. 为什么说"目录位置 ≠ 架构角色"？判断一个包是不是服务看哪三样？

## 代码层

21. 为什么 `internal/rpc` 只有一个 `server.go`？其他服务定义在哪？
22. 7 个服务各自注册的 gRPC 服务全名是什么？
23. 为什么 7 个服务的 Config 形状必须一致？
24. `startrpc.Start` 的 14 个参数可以分成哪几组？
25. 五个内置拦截器分别做什么？为什么 PanicCapture 要在最外层？
26. `getConfigShare` 用反射有什么风险？怎么改更好？
27. 请求体大小限制有四个参数，只配一边会怎样？
28. `rpcli` 为什么这么薄？治理逻辑放在哪？
29. `extractField` 这个泛型函数解决了什么重复？
30. `incrversion` 为什么不是服务？`BatchOption` 的设计精髓是什么？

---

# 第五部分 面试话术

## 30 秒版

> "内部服务间用 gRPC，对外用 HTTP+WebSocket。gRPC 是 IDL + protobuf + HTTP/2 三件套，编译期生成 stub，调用写起来像本地方法。
>
> 选它主要三个原因：protobuf 比 JSON 小且快、有编译期类型检查、治理能力（超时/重试/熔断/负载均衡）框架自带。
>
> 关键点是它强依赖 HTTP/2 的多路复用，所以一条连接能跑几百个并发调用，不需要连接池。"

## 3 分钟版

> "我认为三个点值得说。
>
> **第一是为什么不用 HTTP+JSON 做内部 RPC。** 三个硬伤：性能上 protobuf 比 JSON 小且解析快，HTTP/1.1 还有队头阻塞；类型安全上 HTTP 靠文档约定，gRPC 靠 proto 编译期检查；治理上 HTTP 没有超时传播、负载均衡、熔断的标准做法。但对外 API 我还是用 HTTP+JSON，因为可读、任何语言能调。
>
> **第二是 HTTP/2 带来的架构简化。** 因为多路复用，一条连接能同时跑几百个请求，所以 gRPC 客户端不需要连接池，一个 ClientConn 就够。多实例靠的是 ClientConn 内部的 SubConn + 负载均衡策略，而不是应用层搞多个连接。要注意 HTTP/2 只解决了应用层队头阻塞，TCP 层还在，所以有 HTTP/3。
>
> **第三是治理逻辑的位置。** 项目把日志、错误转换、参数校验、panic 捕获、限流、熔断全做成 gRPC 拦截器，客户端封装保持得很薄。这里有个经典坑：拦截器必须 Unary 和 Stream 各注册一套，只注册 Unary 的话 Stream 接口就完全绕过所有治理。项目里熔断和限流都注册了两套，这点做对了。"

## 追问预判

| 可能被问 | 怎么答 |
|---|---|
| "RPC 超时了怎么办？" | 先要幂等。查询类直接重试；创建类必须带幂等键，否则会重复创建。还要退避+抖动，不然会把慢服务打成挂服务。 |
| "熔断和限流区别？" | 限流保护自己不被流量压垮，看入口量/CPU；熔断保护自己不被下游拖死，看下游失败率。一个在入口一个在出口。 |
| "为什么不用 gRPC 流推消息？" | 浏览器不支持（要 grpc-web 代理），WS 是原生能力；而且长连接管理（心跳、多端踢人）gRPC 流不提供。 |
| "gRPC 错误怎么跨进程传？" | 服务端把错误码和消息编码进 status，客户端解出来还原。要注意两端拦截器必须配对，否则会丢错误码变成 Unknown。 |
| "metadata 会不会被伪造？" | 会。所以 gRPC 端口绝不能暴露公网，内部也要靠 mTLS 证明调用者身份，不能无条件信任 metadata 里的 userID。 |
| "startrpc 用反射有什么问题？" | 没有编译期检查。Config 少了 Share 字段不会报错，只是 admin 功能静默失效。应该定义接口让 7 个 Config 实现，把运行时错误变成编译期错误。 |

---

**下一篇**：[09 服务发现与配置系统](09-服务发现与配置系统.md) —— 服务地址会变，调用方怎么找到它？配置改了要不要重启？
