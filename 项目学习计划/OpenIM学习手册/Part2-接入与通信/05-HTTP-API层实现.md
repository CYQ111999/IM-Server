# 05 HTTP API 层实现：从「一个网页请求」到「153 条接口怎么组织」

> **本篇定位**：这是整个系统对外的"前门"。06 篇讲的是长连接（WebSocket），本篇讲的是**短连接（HTTP）**——注册、登录、加好友、建群、拉资料，全走这里。
>
> **一个必须先说明的结论**：这一层**几乎没有业务逻辑**。它的职责是"收 HTTP 请求 → 转成 gRPC 调用 → 把结果转成 HTTP 响应"。全文 2458 行，绝大部分是"翻译"。**看懂它，等于看懂了整个分层架构的边界。**
>
> **结构**：
> - **第一部分（5.1–5.10）纯理论**：HTTP 报文、方法、状态码、REST、路由、中间件（洋葱模型）、认证与授权、限流算法、统一响应、参数校验、分层。
> - **第二部分（5.11–5.21）源码**：启动流程、11 层中间件、12 个路由组、`a2r.Call` 模式、BBR 限流（含一个 bug）、典型 handler。
> - **第三部分**：实验。
>
> **本篇名词**：HTTP 报文、请求方法、幂等、状态码、REST、路由（Route）、路由组、中间件（Middleware）、洋葱模型、认证/授权、Bearer Token、限流（Rate Limiting）、令牌桶、漏桶、滑动窗口、BBR、Little's Law、统一响应体、错误码、参数绑定与校验。

---

# 第一部分 理论：HTTP 接口是怎么工作的

## 5.1 HTTP 到底是什么：一份有格式的"信"

### 5.1.1 报文结构

HTTP 的本质是**纯文本**（HTTP/1.1）。一次请求就是往 TCP 连接里写一段**有固定格式的字符串**：

```http
POST /auth/get_admin_token HTTP/1.1          ← 请求行：方法 + 路径 + 版本
Host: 127.0.0.1:10002                        ← 请求头开始
Content-Type: application/json
operationID: t1
Content-Length: 62

{"secret":"xxx","userID":"imAdmin"}          ← 空行之后是请求体
```

**请求 = 请求行 + 请求头 + 空行 + 请求体**。四个部分，缺一不可（请求体可以为空，但空行必须有）。

响应同理：

```http
HTTP/1.1 200 OK                              ← 状态行：版本 + 状态码 + 短语
Content-Type: application/json
Content-Length: 245

{"errCode":0,"errMsg":"","data":{...}}       ← 响应体
```

**为什么是文本？** 因为 1991 年设计 HTTP 时，调试方便是第一优先级。人类能直接 telnet 上去手打请求——这个特性至今仍有用（`curl -v` 能看到完整报文）。

### 5.1.2 八个方法（真正用到的只有两个）

| 方法 | 语义 | 幂等 | 有无 body | 本项目用不用 |
|---|---|---|---|---|
| GET | 读取资源 | ✅ | 无 | ✅ 13 条（内部接口） |
| POST | 创建/提交 | ❌ | 有 | ✅ 142 条（主力） |
| PUT | 全量更新 | ✅ | 有 | ❌ |
| DELETE | 删除 | ✅ | 无 | ❌ |
| PATCH | 局部更新 | ❌ | 有 | ❌ |
| HEAD | 只要头 | ✅ | 无 | ❌ |
| OPTIONS | 预检/查询支持的方法 | ✅ | 无 | 中间件自动处理（CORS） |
| TRACE | 回显 | ✅ | 无 | ❌ |

**关键概念：幂等（Idempotent）**

> 同一个请求执行 1 次和执行 N 次，**对服务器状态的影响相同**。

- GET 幂等：查 10 次和查 1 次，数据不变；
- DELETE 幂等：删 1 次和删 10 次，结果都是"没了"（第二次删不存在，但状态一样）；
- **POST 不幂等**：提交 10 次订单，可能创建 10 个订单。

⚠️ **这个区分有实际后果**：网络超时时，幂等请求可以**放心重试**，非幂等请求重试可能造成重复数据。这就是为什么支付、下单接口都要设计"幂等键"（07 篇讲的 `ClientMsgID` 就是消息的幂等键）。

### 5.1.3 状态码：三段数字的含义

| 段 | 含义 | 常见 |
|---|---|---|
| 1xx | 信息性 | 101 Switching Protocols（WS 升级） |
| 2xx | 成功 | 200 OK、201 Created、204 No Content |
| 3xx | 重定向 | 301 永久、302 临时、304 未修改 |
| 4xx | **客户端错** | 400 参数错、401 未认证、403 无权限、404 不存在、429 太频繁 |
| 5xx | **服务端错** | 500 内部错误、502 网关错误、503 不可用、504 超时 |

**4xx 和 5xx 的分界很重要**：
- 4xx = "你的请求有问题，改请求"；
- 5xx = "我的服务有问题，你可以重试"。

**监控告警通常只看 5xx 率**——因为 4xx 是用户的问题，5xx 才是系统的问题。

⚠️ **但 OpenIM 的做法比较特殊**：它在 HTTP 层大量使用 **200 + 业务错误码**。

```json
HTTP/1.1 200 OK
{"errCode":1001,"errMsg":"token 无效","data":null}
```

**为什么？** 见 5.7 的"统一响应体"。这里先记住：**不要以为收到 200 就是成功**，必须看 `errCode`。

---

## 5.2 API 与 REST：接口设计的两种思维

### 5.2.1 什么是 API

**API（Application Programming Interface）= 别人能调用的入口**。对 HTTP API 来说，就是"URL + 方法 + 参数 + 返回格式"的约定。

一个 API 设计要回答四个问题：

1. **叫什么**（URL 路径）
2. **怎么调**（方法、参数放哪）
3. **返回什么**（结构、错误）
4. **怎么演进**（加字段、改语义）

### 5.2.2 REST 的核心思想

**REST（Representational State Transfer）不是协议，是一种设计风格**。核心三条：

**① 用"资源"组织 URL，用"名词"命名**

```
✅ GET  /users/123          拿 123 号用户
✅ POST /groups             创建群
❌ GET  /getUser?id=123     动词 + query
❌ POST /createGroup        动词在路径里
```

**② 用 HTTP 方法表达"动作"**

```
GET    /users/123     查
PUT    /users/123     改
DELETE /users/123     删
```

**③ 用状态码表达结果**

```
200 成功 / 404 不存在 / 403 无权限
```

### 5.2.3 OpenIM 不是 REST——为什么

看真实路由：

```
POST /user/user_register
POST /friend/add_friend
POST /group/create_group
POST /msg/send_msg
```

**全是 POST，路径里全是动词。** 这是典型的 **RPC 风格**（Remote Procedure Call，远程过程调用），不是 REST。

**为什么这么设计？** 四个现实理由：

| 理由 | 说明 |
|---|---|
| **参数复杂** | 一个接口可能有十几个参数、嵌套对象、数组。塞进 URL 不现实，必须放 body |
| **浏览器兼容** | 早期很多网关/防火墙对 PUT/DELETE 支持不好 |
| **批量操作** | "一次拉 100 个用户"用一个 POST 比 100 个 GET 简单 |
| **与 gRPC 对齐** | 每个 HTTP 接口对应一个 gRPC 方法，一对一好映射 |

📌 **面试时不要硬说"这是 RESTful"**。正确说法是：**"它是 RPC 风格的 HTTP API，路径即方法名，全部用 POST，因为参数复杂且要与 gRPC 方法一一对应。"** 这样更专业。

⚠️ **REST 不是"更高级"**。它是为"资源型"场景（内容管理、开放平台）设计的。IM 的接口大多是"动作型"（发消息、踢人、撤回），RPC 风格反而更自然。

---

## 5.3 路由（Route）：URL 怎么找到函数

### 5.3.1 路由是什么

**路由 = URL 模式 → 处理函数的映射表。**

```go
r.POST("/user/user_register", u.UserRegister)
```

这行代码的意思是：当收到 `POST /user/user_register` 时，调用 `UserRegister` 函数。

**框架在背后做的匹配**：把注册的所有模式建成一棵**前缀树（Trie）**，请求来了快速查找。

```
/               (root)
├── user/
│   ├── user_register
│   ├── update_user_info
│   └── get_users_info
├── friend/
└── group/
```

**为什么用 Trie 而不是遍历列表？** 153 条路由，遍历要比较 153 次；Trie 只需按路径段走 2~3 层。**路由数量越多，差距越大**。

### 5.3.2 路由组（Group）：为什么要分组

```go
userRouterGroup := r.Group("/user")
{
    userRouterGroup.POST("/user_register", u.UserRegister)
    userRouterGroup.POST("/update_user_info", u.UpdateUserInfo)
}
```

**三个好处**：

**① 前缀复用**：不用每条都写 `/user/...`。

**② 中间件继承**：组上挂的中间件自动应用于组内所有路由。

```go
adminGroup := r.Group("/config", cm.CheckAdmin)   // 组内全部要管理员
```

**③ 代码组织**：一个业务域一个代码块，好读好维护。

### 5.3.3 路径参数 vs 查询参数 vs 请求体

| 位置 | 形式 | 适合放什么 | 例子 |
|---|---|---|---|
| **路径** | `/object/*name` | 资源标识 | 文件名 |
| **查询串** | `?page=1&size=20` | 过滤、分页、可选参数 | 分页 |
| **请求体** | JSON body | 复杂结构、敏感数据 | 用户信息、token |

⚠️ **一个安全常识**：**敏感数据不要放 URL**。

原因：URL 会被记录在
- 浏览器历史；
- 服务器 access log；
- 代理/CDN 日志；
- Referer 头（点外链时带给第三方）。

**所以 token 放 Header 不放 URL**——除了 WebSocket 那个例外（浏览器 WS API 不支持自定义 Header，只能放 URL，见 06 篇）。

⚠️ **这也是 `GET /object/*name` 这类接口必须用"签名 URL"的原因**：文件访问没法带 Header（浏览器直接打开链接），只能在 URL 里带一个**限时签名**。

---

## 5.4 中间件：HTTP 框架最重要的抽象

### 5.4.1 什么是中间件

**中间件（Middleware）= 在"请求到达业务函数之前"和"响应返回之后"插入的一段通用逻辑。**

典型需求：
- 每个请求都要记日志；
- 每个请求都要验 token；
- 每个请求都要统计耗时；
- 每个请求都要捕获 panic。

**如果不用中间件**，你要在每个 handler 里重复写这些代码：

```go
func UserRegister(c *gin.Context) {
    // 日志
    log.Info("request", c.Request.URL)
    // panic 捕获
    defer func() { if r := recover(); r != nil { ... } }()
    // 验 token
    token := c.Header("token")
    if token == "" { c.JSON(401, ...); return }
    // ... 终于开始业务
}
```

153 个接口 × 4 段重复代码 = **维护灾难**。

**用中间件**：

```go
r.Use(Logger(), Recovery(), Auth())
r.POST("/user/user_register", u.UserRegister)   // handler 里只有业务
```

### 5.4.2 洋葱模型：为什么叫"洋葱"

```
        ┌──────────────────────────┐
        │      请求进入 ↓           │
        │  ┌────────────────────┐  │
        │  │  中间件 A 前置      │  │
        │  │  ┌──────────────┐  │  │
        │  │  │ 中间件 B 前置 │  │  │
        │  │  │  ┌─────────┐ │  │  │
        │  │  │  │ Handler │ │  │  │
        │  │  │  └─────────┘ │  │  │
        │  │  │ 中间件 B 后置 │  │  │
        │  │  └──────────────┘  │  │
        │  │  中间件 A 后置      │  │
        │  └────────────────────┘  │
        │      响应返回 ↑           │
        └──────────────────────────┘
```

**请求按注册顺序"穿过"，响应按相反顺序"穿出"**——像穿过一层层洋葱。

代码上靠 `c.Next()` 实现：

```go
func Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // ① 前置：请求进来时执行
        start := time.Now()

        c.Next()          // ② 交出控制权，执行后续中间件 + handler

        // ③ 后置：响应返回时执行
        log.Info("cost", time.Since(start))
    }
}
```

**`c.Next()` 是关键**：它把控制权交出去，等里面的都执行完再回来。

### 5.4.3 🔴 `Next()` 和 `Abort()` 的陷阱（这个必须理解）

**Gin 的中间件有个反直觉行为**：

> 如果中间件里**既不调用 `c.Next()` 也不调用 `c.Abort()`**，后续 handler **仍然会执行**。

为什么？看 Gin 的核心循环（简化）：

```go
func (c *Context) Next() {
    c.index++
    for c.index < int8(len(c.handlers)) {
        c.handlers[c.index](c)     // 执行当前中间件
        c.index++                   // ← 无论中间件内部做了什么，这里都会继续
    }
}
```

**关键在 `c.index++`**：它在 for 循环里，**不管中间件是否调用了 Next**，循环都会继续。

**所以"拦截请求"必须显式 `Abort()`**：

```go
if token == "" {
    c.JSON(401, ...)
    c.Abort()        // ← 必须！否则后面的 handler 还是会执行
    return
}
```

`Abort()` 做的是 `c.index = abortIndex`（一个很大的数），让 for 循环直接结束。

⚠️ **这是 Gin 最容易踩的坑之一**。写错了会导致：**认证失败但业务逻辑照样执行** → 严重越权。

📌 **记住这个规则**：中间件里"提前返回"必须配 `Abort()`。**5.14 节会遇到一个没有 default 分支的认证中间件，正是这个坑的变种。**

### 5.4.4 中间件的顺序为什么重要

```go
r.Use(Logger(), Recovery(), Auth())
```

**顺序决定了嵌套关系**：

```
Logger
  └── Recovery
        └── Auth
              └── Handler
```

**三条排序原则**：

**① 日志在最外层**：这样才能记录到"被 Recovery 捕获的 panic"和"被 Auth 拒绝的请求"。如果把 Logger 放里面，认证失败的请求就不会被日志记录。

**② Recovery 在 Auth 之外**：Auth 里如果 panic（比如解析 token 时数组越界），也要被捕获。

**③ 认证在业务之前**：废话，但要确认它真的在所有业务路由之前注册。

⚠️ **顺序错的典型症状**：日志里看不到 401 请求 → 说明 Logger 在 Auth 里面。

---

## 5.5 认证 vs 授权：两个必须分清的概念

### 5.5.1 定义

| | 认证（Authentication） | 授权（Authorization） |
|---|---|---|
| 问题 | **你是谁？** | **你能干什么？** |
| 时机 | 先 | 后 |
| 失败返回 | 401 Unauthorized | 403 Forbidden |
| 本项目位置 | **API 层**（`GinParseToken`） | **RPC 层**（`CheckAdmin` / `CheckAccess`） |

**401 vs 403 的区别**：
- 401 = "你没证明身份"（没带 token / token 无效）→ 去登录；
- 403 = "我知道你是谁，但你没权限"（普通用户访问管理接口）→ 别试了。

### 5.5.2 🔴 本项目最重要的一条架构决策

> **认证在 API 层，授权在 RPC 层。**

**为什么这么分？**

**认证放 API 层**：
- 只有 API 层接触 HTTP（能读 Header）；
- gRPC 服务可能被内部其他服务直接调用，不该重复解析 token；
- 在入口统一做，避免每个 RPC 都写一遍。

**授权放 RPC 层**：
- 授权需要**业务数据**（这个群的管理员是谁？这个用户能不能看这条消息？）；
- API 层没有数据库访问能力（它是薄层，不碰存储）；
- 授权逻辑必须离数据近。

**这个分工的直接后果**：

```
API 层：我只保证"调用者是 user001"
RPC 层：我要判断"user001 能不能操作 group123"
```

📌 **面试价值**：能讲清这条边界，说明你理解分层不是"随便切的"，而是**按"谁能拿到所需信息"来切的**。

⚠️ **一个推论**：如果绕过 API 直接调 RPC（比如内部服务），**认证就没了**。所以 RPC 端口绝不能暴露到公网——这是部署红线。

### 5.5.3 Token 怎么传

**标准做法**：`Authorization: Bearer <token>`

```
Authorization: Bearer eyJhbGciOiJIUzI1NiIs...
                     ↑ 固定前缀，表示"持有者令牌"
```

**OpenIM 的做法**：自定义 header `token: <token>`

```go
// router.go:407
token := c.Request.Header.Get(constant.Token)   // constant.Token = "token"
```

**为什么不用标准的 `Authorization`？** 历史原因 + 简单。**功能上没区别**，但标准做法更好（能被网关、SDK 自动识别）。

📌 面试如果被问"你的 token 放哪"，答"自定义 header，标准做法应该是 Authorization: Bearer"——**知道标准但不盲从**，这是加分项。

---

## 5.6 限流：为什么要"主动拒绝用户"

### 5.6.1 一个反直觉的事实

**限流不是为了"限制用户"，是为了"保护系统"。**

想象一个场景：某接口正常 QPS 1000，突然来了 10 万（爬虫、攻击、或者某个客户端 bug 疯狂重试）。

**不限流的后果**：
```
10 万请求涌入
  → 数据库连接池耗尽
  → 所有请求都在等连接（包括正常的 1000 个）
  → 响应时间从 50ms 涨到 30s
  → 客户端超时重试 → 请求量再翻倍
  → 雪崩
```

**限流的后果**：
```
10 万请求涌入
  → 95000 个立刻收到 429（"太频繁，请稍后"）
  → 5000 个正常处理，响应时间 50ms
  → 系统稳定
```

**结论：拒绝 95% 的请求，比让 100% 的请求都超时更好。** 这就是"**快速失败（fail fast）**"哲学。

### 5.6.2 四种经典限流算法

**① 固定窗口计数器**

```
每分钟最多 100 次：
00:00 - 00:59  计数，超过 100 拒绝
01:00  计数清零
```

❌ **临界问题**：在 00:59 打 100 次，01:00 再打 100 次 → **2 秒内 200 次**，突破了限制。

**② 滑动窗口**

把 1 分钟切成 6 个 10 秒的格子，每次统计"最近 6 个格子"的总数。

✅ 解决临界问题；❌ 实现复杂，要存多个格子。

**③ 漏桶（Leaky Bucket）**

```
请求 → [桶] → 恒定速率流出
        ↑
      桶满了就拒绝
```

**特点**：**输出速率绝对恒定**。适合"保护下游"（比如调用第三方 API 有严格 QPS 限制）。

❌ 不适合突发流量（即使系统有空闲，也只能按固定速率处理）。

**④ 令牌桶（Token Bucket）**

```
        恒定速率放令牌
             ↓
        [桶：最多存 N 个令牌]
             ↓
    请求来了 → 有令牌就放行（消耗一个），没有就拒绝/等待
```

**特点**：**允许突发**——桶里攒了 100 个令牌，突然来 100 个请求可以一次性放行。平均速率仍受限。

✅ 最常用（Nginx、Guava RateLimiter 都是这个）。

### 5.6.3 但 OpenIM 用的是 BBR（第五种）

**BBR 原本是 TCP 拥塞控制算法**（Google 2016 年提出），用来决定"发多快"。OpenIM 把它借用来做**服务端自适应限流**。

**核心思路完全不同**：

| | 令牌桶 | BBR |
|---|---|---|
| 限流依据 | **预设的 QPS 上限** | **系统当前的负载**（CPU、延迟） |
| 需要配置什么 | 阈值（1000 QPS） | CPU 阈值（如 80%） |
| 流量涨了怎么办 | 死守 1000，多的拒绝 | **自动探测系统能承受多少** |
| 适合场景 | 有明确配额 | 云环境、负载波动大 |

**BBR 的核心指标**：

```
maxInFlight = 系统能同时处理多少请求（动态计算）
inFlight    = 当前正在处理的请求数
              ↓
inFlight > maxInFlight → 拒绝
```

**`maxInFlight` 怎么算？** 经典 BBR 用 **Little's Law（利特尔法则）**：

```
并发数 = 吞吐量 × 响应时间
L = λ × W
```

BBR 通过观察"最小响应时间（MinRt）"和"最大通过量（MaxPass）"来估算系统的**瓶颈容量**：当响应时间开始上涨（说明排队了），就降低放行速率。

**为什么这比令牌桶好？**

- 令牌桶的阈值是**拍脑袋定的**：定 1000 还是 2000？拍小了浪费资源，拍大了保护不了。
- BBR **不需要人配阈值**，它自己探测。CPU 从 40% 涨到 90% 时，它会自动收紧。

**代价**：
- 算法复杂，行为不容易预测；
- 冷启动阶段估算不准；
- 不适合"必须精确控制 QPS"的场景（比如计费接口）。

📌 **面试价值**：能说出"BBR 是自适应限流，靠 Little's Law 估算系统容量，不需要预设 QPS 阈值"，比只会说"令牌桶"高一个层次。

### 5.6.4 限流放在哪一层

| 位置 | 优点 | 缺点 |
|---|---|---|
| **网关/Nginx** | 保护整个集群，不消耗应用资源 | 粒度粗（只能按 IP/路径） |
| **应用中间件** | 粒度细（可按用户、按接口） | 消耗应用资源 |
| **RPC 层** | 保护具体服务 | 请求已经过了一层 |

**本项目在 API 层中间件做**——因为：
- API 是入口，早拒绝早省资源；
- 能拿到 userID（认证后），可以做"按用户限流"；
- 分布式部署时每个实例独立限流（总量 = 实例数 × 单实例阈值）。

⚠️ **单机限流的局限**：10 个实例，每个限 1000 QPS，总量就是 10000。如果某个用户被均匀打散到 10 个实例，他能拿到 10 倍的配额。**要精确限流必须用 Redis 集中计数**（有性能代价）。

---

## 5.7 统一响应体：为什么所有接口返回同一个壳

### 5.7.1 三种错误表达方案

**方案 A：纯 HTTP 状态码**

```
成功 → 200 + 数据
失败 → 404 + 空
```

❌ 状态码只有几十个，**表达不了业务错误**（"群已解散"用哪个码？"你不是群成员"用哪个码？）。

**方案 B：HTTP 状态码 + 错误信息**

```
400 {"error":"group dismissed"}
```

⚠️ 客户端要**解析英文文案**来判断错误类型 → 文案一改就崩。

**方案 C：统一响应体 + 业务错误码（本项目采用）**

```json
{
  "errCode": 1001,
  "errMsg": "token 无效",
  "data": null
}
```

**`errCode` 是稳定的数字，`errMsg` 是给人看的文案。** 客户端判断 `errCode`，展示 `errMsg`。

### 5.7.2 统一响应体的四个好处

**① 客户端只需一个解析器**

```go
type ApiResponse struct {
    ErrCode int             `json:"errCode"`
    ErrMsg  string          `json:"errMsg"`
    Data    json.RawMessage `json:"data"`
}
```

所有接口都是这个结构，客户端不用为每个接口写不同的解析逻辑。

**② 业务错误不受 HTTP 状态码限制**

"群不存在"、"不是好友"、"被禁言"…… 每个都有自己的错误码，语义精确。

**③ 前端拦截器统一处理**

```js
axios.interceptors.response.use(res => {
    if (res.data.errCode !== 0) {
        if (res.data.errCode === 1501) {  // token 过期
            refreshToken()
        }
        throw new Error(res.data.errMsg)
    }
    return res.data.data
})
```

**一处写，全局生效**。

**④ 监控可以按错误码聚合**

```
errCode=0    成功率
errCode=1001 token 错误数（突增说明 token 签发有问题）
errCode=2003 参数错误数（突增说明客户端版本有 bug）
```

### 5.7.3 代价：状态码信息丢失

⚠️ **统一响应体的最大问题**：HTTP 状态码失去意义，**中间设施看不懂**。

- Nginx 的 access log 里全是 200，看不出错误率；
- 云厂商的监控告警（按 5xx 率）完全失效；
- CDN/网关的熔断策略（按 5xx）无法工作。

**本项目的部分折中**：
- 限流拒绝时**同时**返回 429 状态码和 `errCode`（`ratelimit.go`）；
- 其他业务错误返回 200 + `errCode`。

📌 **更成熟的做法**：**HTTP 状态码表达"协议层结果"，`errCode` 表达"业务层结果"**。

```
参数格式错 → 400 + errCode
未认证     → 401 + errCode
无权限     → 403 + errCode
资源不存在 → 404 + errCode
业务规则冲突 → 200 或 409 + errCode
```

这样两边都满意。

---

## 5.8 参数校验：为什么不能只信客户端

### 5.8.1 三个层次的校验

```
① 前端校验：用户体验（即时提示）      ← 可被绕过
② API 层校验：格式合法性（类型、长度） ← 本篇
③ 业务层校验：语义合法性（权限、状态） ← RPC 层
```

**关键认知：前端校验只是"体验优化"，不是"安全措施"。** 攻击者用 curl 直接打接口，前端校验完全不生效。

### 5.8.2 声明式校验：struct tag

```go
type RegisterReq struct {
    Nickname string `json:"nickname" binding:"required,min=1,max=32"`
    Password string `json:"password" binding:"required,min=6,max=20"`
    Phone    string `json:"phone"    binding:"omitempty,phone"`
}
```

**`binding` tag 就是一串规则**：
- `required`：必填（零值则失败）；
- `min=1,max=32`：长度/数值范围；
- `omitempty`：为空则跳过后续校验；
- `phone`：自定义规则（需要注册）。

**框架在 `ShouldBindJSON` 时自动执行**，失败返回 error。

### 5.8.3 自定义校验器

标准 tag 不够用时，可以注册自定义规则：

```go
v.RegisterValidation("required_if", RequiredIf)
```

```go
Role string `binding:"required_if=Type admin"`   // Type=admin 时 Role 必填
```

📌 **"条件必填"是很常见的需求**（比如"发群消息时 GroupID 必填，发单聊时 RecvID 必填"），标准 tag 表达不了，必须自定义。

### 5.8.4 校验的三条实践原则

**① 校验在入口做，不要漏到业务层**

参数格式错误应该返回 400，而不是让业务代码 panic 或写到数据库才发现。

**② 错误信息要能定位**

```json
{"errCode":2003,"errMsg":"password: 长度不能少于 6"}
```

要指出**哪个字段、什么规则**，而不是笼统的"参数错误"。

**③ 不要在 tag 里放业务规则**

```
✅ min=6,max=20          格式约束
❌ "密码不能和用户名相同"  业务规则 → 放业务层
```

**因为业务规则会变，而且往往需要查数据库。**

---

## 5.9 分层：为什么 API 层要"薄"

### 5.9.1 两种分层风格

**风格 A：厚 API 层（业务写在 handler 里）**

```go
func UserRegister(c *gin.Context) {
    var req RegisterReq
    c.ShouldBindJSON(&req)

    // 查重
    if db.Where("phone = ?", req.Phone).First(&user).Error == nil { ... }
    // 加密
    hashed, _ := bcrypt.GenerateFromPassword(...)
    // 写库
    db.Create(&User{...})
    // 发 token
    token := jwt.Sign(...)

    c.JSON(200, ...)
}
```

**风格 B：薄 API 层（业务在 RPC 层）**

```go
func (o *UserApi) UserRegister(c *gin.Context) {
    a2r.Call(c, user.UserClient.UserRegister, o.Client)
}
```

**本项目是 B**，而且薄到极致——**大部分 handler 只有一行**。

### 5.9.2 为什么选薄

| 理由 | 说明 |
|---|---|
| **多协议复用** | 同样的业务要被 HTTP、WebSocket、gRPC 调用。业务写在 handler 里，WS 要重写一遍 |
| **可独立扩容** | API 层和 RPC 层可以分别扩缩容（API 抗连接，RPC 抗计算） |
| **职责单一** | API 层只管协议转换，RPC 层只管业务，改一边不影响另一边 |
| **可测试** | 业务逻辑在 RPC 层，直接测 gRPC 方法，不用起 HTTP |

### 5.9.3 薄的代价

**① 多一跳网络**

```
客户端 → API → RPC → DB
```

比"API 直接查库"多一次内网 RPC（约 0.5~2ms）。**在 IM 场景可接受**（相比网络往返几十 ms）。

**② 调试要跨进程**

以前一个断点能看完，现在要在 API 和 RPC 两处打断点。**靠 operationID 串联日志**（5.15 讲）。

**③ 容易出现"空壳函数"**

```go
func (o *UserApi) GetUsersInfo(c *gin.Context) {
    a2r.Call(c, user.UserClient.GetUsersInfo, o.Client)
}
```

**153 个这样的函数**，看起来像"没干活"。但这是**刻意的架构选择**，不是偷懒。

📌 **面试时如果被问"你的 API 层做了什么"，不要答"就是转发"**。要答：

> "它做了四件必须做的事：① 协议转换（HTTP ↔ gRPC）② 认证（token 只在入口解一次）③ 参数绑定与格式校验 ④ 统一响应和错误码。业务逻辑故意不下沉到这里，因为同一套业务还要被 WebSocket 和 gRPC 复用。"

---

## 5.10 理论部分小结

```
HTTP 是一份有格式的文本：请求行 + 头 + 空行 + 体
    ↓
API 设计有两种风格：REST（资源+方法）vs RPC（路径即方法名）
    ↓ 本项目是 RPC 风格，全 POST，因为参数复杂且要与 gRPC 对齐
URL 靠路由表（Trie）找到函数，靠路由组做前缀复用和中间件继承
    ↓
中间件是洋葱模型：前置 → Next() → 后置
    ↓ 陷阱：不调 Abort() 就拦不住请求
认证（你是谁，API 层）与授权（你能干什么，RPC 层）必须分开
    ↓ 因为授权需要业务数据，而 API 层不碰存储
限流是保护系统，不是限制用户；BBR 是自适应限流，不需要配 QPS 阈值
    ↓
统一响应体（errCode + errMsg + data）让客户端只写一个解析器
    ↓ 代价是 HTTP 状态码失去意义，监控要按 errCode 聚合
参数校验分三层：前端（体验）、API 层（格式）、RPC 层（业务）
    ↓
薄 API 层：多一跳 RPC 的代价，换来多协议复用和独立扩容
```

**第二部分开始，逐层拆开这 2458 行代码。**

---

# 第二部分 源码：拆开这 2458 行

## 5.11 启动链路：从 main 到 ListenAndServe

```
cmd/openim-api/main.go:9          cmd.NewApiCmd().Exec()
  └─ pkg/common/cmd/api.go:62      (*ApiCmd).runE()
       └─ startrpc.Start(...)      pkg/common/startrpc/start.go:36
            ├─ 建服务发现客户端
            ├─ 装 gRPC 客户端拦截器（日志/上下文/错误转换/负载均衡）
            ├─ 起 Prometheus 监听
            └─ 回调 api.Start       internal/api/init.go:29
                 ├─ 取端口（按 Index 选，支持多实例）
                 ├─ newGinRouter()  internal/api/router.go:61
                 └─ http.Server.ListenAndServe()
```

### `api.Start` 的签名里有一个"没用的参数"

```go
// init.go:29
func Start(ctx context.Context, config *Config, client discovery.SvcDiscoveryRegistry, service grpc.ServiceRegistrar) error
```

**`service grpc.ServiceRegistrar` 这个参数，函数体内从未使用。**

对比 `internal/rpc/msg/server.go` 里真正的服务注册：

```go
msg.RegisterMsgServer(server, s)    // ← 这才是注册 gRPC 服务
```

**结论**：**API 进程不注册任何 gRPC 服务**。它只是 `startrpc` 统一启动器的产物——启动器对所有服务用同一个回调签名，API 用不上这个参数而已。

📌 **这是一个"读代码能发现的事实"**：不要看到参数就以为它有用。**验证方式是搜函数体里有没有使用**。

### 端口选择：为什么是 `GetElemByIndex`

```go
apiPort, err := datautil.GetElemByIndex(config.API.Api.Ports, int(config.Index))
```

配置文件里 `ports: [10002]` 是一个**数组**。`Index` 是实例序号（多实例部署时通过命令行/环境变量传入）。

**为什么不用随机端口或环境变量？**
- 数组 + Index 可以**从同一份配置启动多实例**（Index=0 用 10002，Index=1 用 10003）；
- 端口在配置文件里可见，运维不用记环境变量。

📌 **这与"配置即契约"的理念一致**：所有部署形态都由配置文件描述，不靠隐式约定。

### 优雅关闭

```go
go func() {
    select {
    case <-ctx.Done():              // 收到退出信号
        apiCancel(...)
    case <-apiCtx.Done():
    }
    httpServer.Shutdown(context.Background())   // ← 优雅关闭
}()
```

**`Shutdown` 和 `Close` 的区别**：
- `Close()`：立刻断开所有连接，正在处理的请求被中断；
- `Shutdown(ctx)`：**停止接受新连接，等待正在处理的请求完成**（有超时）。

**为什么重要？** 滚动发布时，如果没有优雅关闭，每次重启都会有一批用户请求失败。`Shutdown` 让"重启"对用户无感。

⚠️ **注意这里用的是 `context.Background()`**，没有超时——如果某个请求永久卡住，`Shutdown` 会一直等。外层有一个 15 秒的兜底定时器（`timer := time.NewTimer(time.Second * 15)`），超时后强制退出。

---

## 5.12 中间件链：11 层，顺序就是执行顺序

`router.go:90-121` 的注册顺序：

| # | 中间件 | 位置 | 作用 | 对应理论 |
|---|---|---|---|---|
| 0 | `gin.New()` | `:91` | 不用 `gin.Default()`，Logger/Recovery 手动挂 | — |
| 1 | 自定义校验器 `required_if` | `:92-94` | 跨字段校验 | 5.8.3 |
| 2 | gzip | `:95-103` | 按 `compressionLevel` 决定是否压缩 | — |
| 3 | **BBR 限流** | `:106-114` | 进程级自适应限流 | 5.6.3 |
| 4 | 注入管理员名单（standalone） | `:116-120` | 把 admin 列表塞 ctx | — |
| 5 | `GinLogger()` | `:121` | 访问日志 | 5.4.4 |
| 6 | `prommetricsGin()` | `:121` | Prometheus 指标 | — |
| 7 | `RecoveryWithWriter` | `:121` | panic 捕获 | 5.4.4 |
| 8 | `CorsHandler()` | `:121` | 跨域 | — |
| 9 | `GinParseOperationID()` | `:121` | 链路追踪 ID | 5.15 |
| 10 | **`GinParseToken()`** | `:121` | **认证** | 5.5 |
| 11 | `setGinIsAdmin()` | `:121` | 标记管理员名单 | — |

### 三个顺序细节（都能讲出理由）

**① gzip 在限流之前**

```go
r.Use(gzip.Gzip(...))        // 先压缩
r.Use(RateLimitMiddleware(rl))  // 再限流
```

**为什么？** 因为限流的响应（429）也应该被压缩。如果反过来，被限流的响应就不压缩了。

⚠️ 但严格说，**压缩应该在最后**（写完响应才压）。Gin 的 gzip 中间件内部是 `c.Next()` 之后再压，所以这里注册顺序靠前，实际压缩动作仍在最后。**注册顺序 ≠ 执行顺序**，要区分"前置逻辑"和"后置逻辑"。

**② 限流在认证之前**

```go
r.Use(RateLimitMiddleware(rl))   // :106
r.Use(GinParseToken(...))        // :121
```

**为什么？** 因为**认证要调 auth 服务（一次 gRPC）**。如果先认证再限流，那么被限流的请求也白花了一次 RPC。

**这和 06 篇握手时"先查连接数上限再鉴权"是同一个思路**：**越便宜的检查越靠前**。

**③ standalone 模式下注入 admin 名单**

```go
if config.Standalone() {
    r.Use(func(c *gin.Context) {
        c.Set(authverify.CtxAdminUserIDsKey, cfg.Share.IMAdminUser.UserIDs)
    })
}
```

**`Standalone()` 是什么？** 单进程一体化部署模式（`cmd/main.go` 里 12 个服务跑在一个进程）。

**为什么这个模式下要特殊处理？** 见 5.14.4。

---

## 5.13 路由分组：12 个业务域

`router.go:126-337`：

| 行号 | 分组 | 业务域 | 典型接口 |
|---|---|---|---|
| `:126` | `/user` | 用户 | 注册、改资料、批量查用户 |
| `:159` | `/friend` | 好友关系 | 加好友、同意、拉黑、好友列表 |
| `:186` | `/group` | 群组 | 建群、邀请、踢人、群成员 |
| `:223` | `/auth` | 认证 | 获取 token、解析 token |
| `:233` | `/third` | 第三方 | 对象存储签名、日志上传、推送 token |
| `:238` | `/third/logs` | 日志子组 | 客户端日志上传 |
| `:243` | `/object` | 对象存储重定向 | 文件访问（**GET**） |
| `:258` | `/msg` | 消息 | 发消息（管理端）、撤回、拉历史 |
| `:284` | `/conversation` | 会话 | 会话列表、已读、置顶 |
| `:301` | `/statistics` | 统计 | 用户/消息统计 |
| `:311` | `/jssdk` | 前端 SDK | 签名等 |
| `:317` | `/prometheus_discovery` | 服务发现 | 给 Prometheus 用（**GET**） |
| `:337` | `/config` | 配置 | **挂了 `cm.CheckAdmin`** |

### 三个值得注意的细节

**① 只有 `/config` 组挂了授权中间件**

```go
configGroup := r.Group("/config", cm.CheckAdmin)
```

**其他所有组都没有授权中间件**——这印证了 5.5.2 的架构决策：**授权在 RPC 层**。

`/config` 是例外，因为它**直接操作配置**（不走 RPC 业务），所以只能在 API 层做权限判断。

📌 **这是"例外验证规则"的好例子**：如果所有接口都在 RPC 层授权，那唯一在 API 层授权的接口，一定是因为它不经过 RPC。**读代码时找例外，能反推出架构意图。**

**② 统计接口还在，但 `pkg/statistics` 已删**

`/statistics` 分组存在，但它的实现在 `internal/api/statistics.go`，调用的是 RPC 服务。**瘦身时删的是 `pkg/statistics`（计数工具包），不是这个 API 分组**——两者同名但无关。

⚠️ 这是一个容易误解的点：**同名 ≠ 同一个东西**。读代码要确认包路径。

**③ `/object` 用的是 `GET /*name`**

```go
objectGroup.GET("/*name", t.ObjectRedirect)
```

**通配符 `*name` 匹配任意路径**。比如 `GET /object/avatars/user001.png`。

**为什么用 GET 且不走 token 认证？** 见 5.14.3——它靠**签名 URL** 保护。

---

## 5.14 认证：`GinParseToken` 的完整逻辑与一个隐患

### 5.14.1 源码

```go
// router.go:396-425
func GinParseToken(authClient *rpcli.AuthClient) gin.HandlerFunc {
    return func(c *gin.Context) {
        switch c.Request.Method {
        case http.MethodPost:                       // ← 只处理 POST
            for _, wApi := range Whitelist {
                if strings.HasPrefix(c.Request.URL.Path, wApi) {
                    c.Next()                        // 白名单直接放行
                    return
                }
            }

            token := c.Request.Header.Get(constant.Token)
            if token == "" {
                apiresp.GinError(c, servererrs.ErrArgs.WrapMsg("header must have token"))
                c.Abort()                           // ← 正确使用了 Abort
                return
            }

            resp, err := authClient.ParseToken(c, token)   // ← gRPC 调 auth
            if err != nil {
                apiresp.GinError(c, err)
                c.Abort()
                return
            }

            c.Set(constant.OpUserPlatform, constant.PlatformIDToName(int(resp.PlatformID)))
            c.Set(constant.OpUserID, resp.UserID)          // ← 把 userID 塞进上下文
            c.Next()
        }
    }
}
```

### 5.14.2 白名单

```go
// router.go:434
var Whitelist = []string{
    "/auth/get_admin_token",
    "/auth/parse_token",
    ...
}
```

**白名单 = 不需要 token 的接口**。典型：
- 获取 token 的接口（你还没有 token，怎么带 token？）；
- 解析 token 的接口（它的作用就是验 token）。

⚠️ **白名单是安全审计的重点**。任何加进白名单的接口都**完全公开**，必须确认它不含敏感数据、且有其他保护（如 secret 校验）。

### 5.14.3 🔴 隐患：只处理 POST，且没有 default 分支

**这是本篇最重要的发现。**

看这个 switch：

```go
switch c.Request.Method {
case http.MethodPost:
    // ... 全部认证逻辑
}
// ← 没有 default，没有 else
```

**如果请求方法不是 POST，会发生什么？**

按理论 5.4.3 讲的 Gin 行为：**中间件不调 `Abort()` 时，后续 handler 仍会执行**。

所以：

```
GET /some/protected/api
  → GinParseToken 的 switch 不匹配
  → 函数直接返回（没有 Abort）
  → 后续 handler 照常执行
  → ctx 里没有 userID
  → 业务逻辑拿不到调用者身份
```

**这算不算漏洞？** 要看有没有 GET 业务路由。实测结果：

```
POST: 142 条
GET:  13 条
```

**13 条 GET 路由具体是**：
- `GET /third/prometheus`（`:234`）：Grafana 数据源，内部监控用；
- `GET /object/*name`（`:253`）：文件重定向，靠签名 URL；
- `GET /prometheus_discovery/*`（`:318-325`）：给 Prometheus 做服务发现的 8 个接口。

**结论（要准确表述）**：
- **目前没有 GET 的业务接口**，所以这个缺口**暂时不可利用**；
- 但这 13 个接口**确实完全绕过了 token 校验**，安全性依赖：① 部署时用网络策略隔离（这些接口不该暴露公网）② `/object` 靠签名 URL 自校验；
- **如果以后有人新增一个 `GET /user/profile`，会直接绕过认证**——这是一个**明确的架构隐患**。

📌 **这是很好的面试素材**：
> "我读 API 层时发现认证中间件只处理 POST，而且没有 default 分支。按 Gin 的语义，不调 Abort 的话后续 handler 仍会执行，所以非 POST 请求实际上绕过了认证。目前只有 13 个内部 GET 接口，没有业务影响，但这是一个隐患——如果新增 GET 业务接口就会越权。修法很简单：加 default 分支返回 405，或者把 switch 改成 if 判断。"

⚠️ **但表述要保守**：我在文章里说"暂时不可利用"，是因为我确认了没有 GET 业务路由。**如果你实际部署时加了反向代理或自定义路由，结论可能变化**——这是部署相关问题，不是代码问题。

### 5.14.4 为什么 Standalone 模式要注入 admin 名单

```go
if config.Standalone() {
    r.Use(func(c *gin.Context) {
        c.Set(authverify.CtxAdminUserIDsKey, cfg.Share.IMAdminUser.UserIDs)
    })
}
```

**背景**：`authverify.IsAdmin(ctx)` 需要知道"谁是管理员"。有两种来源：
- **RPC 层**：从配置里读（`Share.IMAdminUser.UserIDs`）；
- **API 层**：需要把这份名单传下去。

**为什么 standalone 要单独注入？** 因为在单进程模式下，API 和 RPC 虽然在同一进程，但**上下文不共享**（是不同的 goroutine，各自有 context）。所以 API 层要把名单塞进 ctx，随请求传给 RPC。

📌 **这暴露了一个设计问题**：管理员名单本该由 RPC 层自己从配置读，不该靠上游传。**"上游传下来的权限信息"是可以被伪造的**（如果 API 层有 bug）。这是"信任边界"的问题——**下游服务不应该信任上游传来的权限判断，只应该信任它传来的身份**。

**更安全的做法**：API 只传 userID，RPC 自己判断是不是管理员。

### 5.14.5 认证后做了什么：往 ctx 塞两个值

```go
c.Set(constant.OpUserPlatform, constant.PlatformIDToName(int(resp.PlatformID)))
c.Set(constant.OpUserID, resp.UserID)
```

**`c.Set` 是 Gin 的上下文存储**（本质是 `map[string]any`，带读写锁）。后续 handler 通过 `c.GetString(constant.OpUserID)` 拿到当前用户。

**为什么用 ctx 而不是函数参数？**
- handler 签名固定（`func(*gin.Context)`），加不了参数；
- ctx 还能跨中间件传递。

⚠️ **代价：类型不安全**。`c.Get` 返回 `any`，取出来要断言；key 拼错了不会报错（返回 nil）。**这是 Gin 的设计缺陷**，所有用 Gin 的项目都有这个问题。

---

## 5.15 `operationID`：跨进程链路追踪

### 5.15.1 问题：一条请求跨了 3 个进程，日志怎么串起来

```
客户端 → API（进程 A）→ RPC（进程 B）→ DB
```

如果出错，你要在三个进程的日志里找"这次请求"。**怎么找？**

**答案：给每个请求分配一个唯一 ID，所有日志都带上它。**

```go
// 中间件 GinParseOperationID
c.Set(constant.OpUserID, ...)
```

这个 ID 就是 **`operationID`**（也叫 traceID / requestID）。

### 5.15.2 它怎么跨进程传

```
① 客户端在 HTTP header 里带 operationID（或由 API 层生成）
② API 层塞进 ctx
③ 调 gRPC 时，通过 gRPC metadata（相当于 HTTP header）传给 RPC
④ RPC 层从 metadata 取出，继续往下传
⑤ 每一层打日志都带上它
```

**gRPC 的 metadata 就是"跨进程的 ctx"**：

```go
// 客户端
ctx = metadata.AppendToOutgoingContext(ctx, "operationID", opID)

// 服务端
md, _ := metadata.FromIncomingContext(ctx)
opID := md.Get("operationID")[0]
```

**为什么不用全局变量？** 因为并发请求会互相覆盖。**必须随 context 传递**（context 天然是请求级的）。

📌 **面试价值**：能讲清"分布式追踪靠一个 requestID 贯穿所有日志"，是后端基本功。更完整的方案是 **OpenTelemetry**（traceID + spanID + 父子关系），但原理相同。

⚠️ **本项目没有用 OpenTelemetry**，是自制的 operationID 透传。**更简单，但缺少 span 层级**（无法知道"这次请求在 RPC 层花了多少时间、DB 花了多少"）。

---

## 5.16 `a2r.Call`：一行代码完成的"翻译"

### 5.16.1 全貌

```go
// internal/api/auth.go:18
a2r.Call(c, auth.AuthClient.GetAdminToken, o.Client)
```

**`a2r` 是什么？** 名字应该是 **API to RPC** 的缩写。它是 `github.com/openimsdk/tools` 里的泛型工具函数。

### 5.16.2 它做了什么（四步）

```
① 从 *gin.Context 里读请求体，反序列化成 gRPC 请求结构
② 把 ctx 里的 operationID/userID 等信息塞进 gRPC metadata
③ 调用 gRPC 方法
④ 把 gRPC 响应转成统一响应体，写回 HTTP
```

**用泛型实现的关键**：`a2r.Call` 能接受任意签名的 gRPC 方法（`func(ctx, *Req) (*Resp, error)`），靠**类型参数推断**。

**所以你看到的 handler 只有一行**，因为四步全被封装了。

### 5.16.3 这个封装的收益与代价

**收益**：

| 收益 | 说明 |
|---|---|
| **一致性** | 153 个接口的"参数解析、错误处理、响应格式"完全一致，不会有人写错 |
| **减少样板** | 每个接口省 20 行代码，153 个就是 3000 行 |
| **改一处全局生效** | 比如要加"所有响应都加 traceID"，改 `a2r` 就行 |

**代价**：

| 代价 | 说明 |
|---|---|
| **可读性下降** | 看不出 handler 具体做了什么，要跳到 `a2r` 源码 |
| **灵活度受限** | 需要特殊处理的接口（如文件上传流式转发）无法用这个模式 |
| **调试困难** | 反序列化出错时，堆栈在 `a2r` 里，看不出是哪个接口 |

📌 **这是典型的"抽象"权衡**：**用灵活性换一致性**。对于"153 个几乎一样的接口"，这个交换非常划算。

### 5.16.4 反例：不走 `a2r` 的接口

`/object` 和 `/third/logs` 这类**流式/文件**接口就没法用 `a2r`（它们不是"请求-响应"模式，而是流）。

**这印证了理论**：抽象总有边界，边界外要手写。

---

## 5.17 BBR 限流的实现，以及一个明确的 bug

### 5.17.1 初始化

```go
// router.go:106-114
if cfg.API.RateLimiter.Enable {
    rl := &RateLimiter{
        Enable:       cfg.API.RateLimiter.Enable,
        Window:       cfg.API.RateLimiter.Window,
        Bucket:       cfg.API.RateLimiter.Bucket,
        CPUThreshold: cfg.API.RateLimiter.CPUThreshold,
    }
    r.Use(RateLimitMiddleware(rl))
}
```

```go
// ratelimit.go
limiter := bbr.NewBBRLimiter(
    bbr.WithWindow(config.Window),          // 统计窗口
    bbr.WithBucket(config.Bucket),          // 窗口内分桶数
    bbr.WithCPUThreshold(config.CPUThreshold),  // CPU 阈值（0-1000）
)
```

**三个参数的含义**：

| 参数 | 含义 | 调大的效果 |
|---|---|---|
| `Window` | 统计窗口时长 | 反应更迟钝（平滑但慢） |
| `Bucket` | 窗口内分几个桶 | 统计更精细（但内存/计算更多） |
| `CPUThreshold` | CPU 超过多少就收紧 | 系统更"激进"地用满 CPU |

### 5.17.2 中间件本体

```go
return func(c *gin.Context) {
    status := limiter.Stat()

    // 把 BBR 内部状态写进响应头（调试用）
    c.Header("X-BBR-CPU", ...)
    c.Header("X-BBR-MinRT", ...)
    c.Header("X-BBR-MaxPass", ...)
    c.Header("X-BBR-MaxInFlight", ...)
    c.Header("X-BBR-InFlight", ...)

    done, err := limiter.Allow()      // ← 判断是否放行
    if err != nil {
        c.Header("Retry-After", calculateBBRRetryAfter(status))
        c.AbortWithStatus(http.StatusTooManyRequests)
        apiresp.GinError(c, errs.NewCodeError(429, "too many requests..."))
        return
    }

    c.Next()
    done(ratelimit.DoneInfo{})        // ← 通知 limiter "这个请求处理完了"
}
```

**`done(ratelimit.DoneInfo{})` 的作用很关键**：BBR 需要知道每个请求的**实际耗时**，才能计算出系统的真实容量。`done` 是一个回调，把统计信息喂回去。

⚠️ **如果忘了调 `done`**，BBR 会认为这个请求永远在处理中，`InFlight` 只增不减 → **很快就把自己限死**。这是 BBR 类算法最容易踩的坑。

### 5.17.3 🔴 一个明确的 bug：`loadRatio` 自己除自己

```go
// ratelimit.go:69
func calculateBBRRetryAfter(status bbr.Stat) string {
    loadRatio := float64(status.CPU) / float64(status.CPU)   // ← 恒等于 1.0！

    if loadRatio < 0.8 {
        return "1"
    }
    if loadRatio < 0.95 {
        return "2"
    }

    backoff := 1 + int64(math.Pow(loadRatio-0.95, 2)*50)
    if backoff > 5 {
        backoff = 5
    }
    return strconv.FormatInt(backoff, 10)
}
```

**`status.CPU / status.CPU` 恒等于 1.0**（只要 CPU 不为 0），所以：
- `loadRatio < 0.8` 永远 false；
- `loadRatio < 0.95` 永远 false；
- 永远走最后一段：`backoff = 1 + (1.0-0.95)² × 50 = 1 + 0.0025 × 50 = 1.125` → `int64` 截断 → **1**。

**实际效果**：`Retry-After` 永远是 `"1"` 秒。

**本意应该是** `status.CPU / status.MaxInFlight` 或 `status.InFlight / status.MaxInFlight`（负载率 = 当前负载 / 容量）。

**为什么我能确定这是 bug 而不是有意为之？** 三个证据：
1. 后面的阈值判断（0.8、0.95）**在恒为 1.0 时永远不会触发**，说明这些分支是死代码；
2. 变量名叫 `loadRatio`（负载率），而"CPU / CPU"不是负载率；
3. 计算式 `(loadRatio - 0.95)² × 50` 显然期望 `loadRatio` 在 0.95~1.0+ 之间变化，暗示它应该是一个"利用率"。

📌 **这是"读代码找 bug"的完美案例**：
> "限流中间件的退避时间计算里，`loadRatio` 写成了 `CPU / CPU`，恒等于 1，导致 0.8 和 0.95 两个分支都是死代码，Retry-After 永远返回 1 秒。本意应该是 `InFlight / MaxInFlight`。这个 bug 不影响限流本身是否生效，只影响退避提示的准确性。"

⚠️ **注意影响范围**：这个 bug **不影响"是否限流"**（`limiter.Allow()` 是库里的逻辑），只影响 `Retry-After` 头的值。**说清影响范围，比夸大 bug 更专业。**

---

## 5.18 响应封装：`apiresp`

### 5.18.1 状态码直方图

本项目的响应分布：

| 场景 | HTTP 状态 | `errCode` |
|---|---|---|
| 成功 | 200 | 0 |
| 参数错误 | **200** | 非 0（如 2003） |
| token 无效 | **200** | 非 0（如 1501） |
| 无权限 | **200** | 非 0 |
| 被限流 | **429** | 429 |
| panic | 500（Recovery 处理） | 非 0 |

**只有限流和 panic 用了非 200**。这印证了理论 5.7.3 说的"状态码信息丢失"。

### 5.18.2 `GinError` 做了什么

```go
apiresp.GinError(c, err)
```

**职责**：
1. 从 `err` 里提取**错误码**和**错误信息**（这依赖 `errs` 包的错误包装）；
2. 序列化成统一响应体；
3. 写出 HTTP 响应。

**关键：错误码从哪来？**

```go
servererrs.ErrArgs.WrapMsg("header must have token")
//            ↑ 带错误码的预定义错误    ↑ 附加信息
```

`servererrs` 里每个错误都有：
```go
ErrArgs = errs.NewCodeError(2003, "args error")
```

`WrapMsg` **保留错误码，替换/附加消息**。这就是理论 5.7.1 方案 C 的实现——**数字码稳定，文案可定制**。

### 5.18.3 `prommetricsGin`：按 errCode 聚合

```go
if resp := apiresp.GetGinApiResponse(c); resp != nil {
    prommetrics.APICall(path, c.Request.Method, resp.ErrCode)
}
```

**注意它取的是 `resp.ErrCode`，不是 HTTP 状态码**。

**这就是理论 5.7.2 说的"监控可以按错误码聚合"的实现**。因为状态码全是 200，只有 errCode 有信息量。

⚠️ **但同时它又统计了 404**：

```go
if c.Writer.Status() == http.StatusNotFound {
    prommetrics.HttpCall("<404>", ...)
}
```

**为什么单独统计 404？** 因为 404 表示"路由不存在"，可能是：
- 客户端调了错误的接口（版本不匹配）；
- 攻击者在扫描路径。

**这是一个安全信号**，值得单独监控。

---

## 5.19 自定义校验器：`required_if`

```go
// router.go:92-94
if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
    _ = v.RegisterValidation("required_if", RequiredIf)
}
```

### 它解决什么问题

**场景**：一个接口既能发单聊也能发群聊：

```go
type SendMsgReq struct {
    SessionType int    // 1=单聊 2=群聊
    RecvID      string // 单聊必填
    GroupID     string // 群聊必填
}
```

**标准 tag 表达不了"根据另一个字段决定是否必填"**。`required` 是"永远必填"。

**自定义规则**：

```go
RecvID  string `binding:"required_if=SessionType 1"`
GroupID string `binding:"required_if=SessionType 2"`
```

**语义**：当 `SessionType == 1` 时，`RecvID` 必填。

### 实现原理

```go
func RequiredIf(fl validator.FieldLevel) bool {
    // ① 解析参数："SessionType 1"
    params := strings.Split(fl.Param(), " ")
    // ② 取另一个字段的值
    other := fl.Parent().FieldByName(params[0])
    // ③ 比较
    if fmt.Sprint(other.Interface()) == params[1] {
        return fl.Field().String() != ""    // 条件成立时才校验非空
    }
    return true                              // 条件不成立，直接通过
}
```

📌 **这是"声明式校验"的扩展点**。任何"跨字段"的规则都可以这样加，比在 handler 里写 if-else 优雅得多。

⚠️ **注意**：`FieldByName` 只支持**同层字段**。如果要跨嵌套结构（比如 `req.User.Phone`），需要额外处理路径——这是自定义校验器的常见局限。

---

## 5.20 两个典型 handler 精读

### 5.20.1 最简单的：`GetAdminToken`

```go
// internal/api/auth.go:18
func (o *AuthApi) GetAdminToken(c *gin.Context) {
    a2r.Call(c, auth.AuthClient.GetAdminToken, o.Client)
}
```

**就这一行。** 但它背后发生了：

```
① a2r 读取 c 的 body → 反序列化成 pbAuth.GetAdminTokenReq
② 从 ctx 取 operationID → 塞进 gRPC metadata
③ 调 auth 服务的 GetAdminToken
④ auth 服务校验 secret → 签发 JWT
⑤ a2r 把响应转成 {errCode, errMsg, data} 写回
```

**这个接口在白名单里**（不需要 token）——因为它的作用就是"拿 token"。

**它自己怎么防滥用？** 靠 `secret`（一个共享密钥，配置在 `Share.Secret`）。**没有 secret 就拿不到 admin token**。

⚠️ **所以 `secret` 是最高权限凭据**，泄露等于整个系统沦陷。这就是为什么凭据文件不能进仓库（你的红线第 4 条）。

### 5.20.2 稍复杂的：`UserRegister`

```go
// 大致结构（以实际代码为准）
func (o *UserApi) UserRegister(c *gin.Context) {
    a2r.Call(c, user.UserClient.UserRegister, o.Client)
}
```

**同样是一行**，但注册的业务逻辑全在 RPC 层：

```
user RPC 的 UserRegister：
  ① 校验参数（昵称、密码强度）
  ② 查重（手机号/邮箱是否已注册）
  ③ 生成 userID
  ④ bcrypt 加密密码
  ⑤ 写 Mongo
  ⑥ 建默认会话/好友关系
  ⑦ 签发 token
```

📌 **这正是"薄 API 层"的价值**：注册逻辑只写一遍，HTTP 调它、WebSocket 调它、内部服务也调它，全都是同一个实现。

### 5.20.3 `/msg/send_msg` 为什么是管理端的

```go
// internal/api/msg.go:278
func (o *MsgApi) SendMessage(c *gin.Context) {
    // 要求管理员
    if !authverify.IsAdmin(c) { ... }
    a2r.Call(c, msg.MsgClient.SendMsg, o.Client)
}
```

**普通用户发消息不走这个接口**，而是走 **WebSocket**（06 篇）。

**为什么？**

| | HTTP `/msg/send_msg` | WebSocket 发消息 |
|---|---|---|
| 连接 | 每次新建/复用 HTTP | 长连接，已建立 |
| 延迟 | 多一次 TCP/TLS + HTTP 头 | 一帧直达 |
| 服务端主动回推 | 做不到 | 天然支持 |
| 适合 | **管理端/服务端集成**（低频、批量、可容忍延迟） | **真实用户**（高频、低延迟） |

**这是"同一业务、两种接入方式"的典型**：
- 服务端发通知 → HTTP（简单、无需维护连接）；
- 用户聊天 → WebSocket（低延迟、双向）。

📌 **面试如果被问"为什么不用 HTTP 发消息"，这就是完整答案**。注意**不是"HTTP 做不到"，而是"HTTP 做不好"**——延迟高、无法回推。

---

## 5.21 设计评价

### 做得好的三处

**① 认证与授权的分层边界清晰**

认证在 API（能读 Header）、授权在 RPC（能读数据）。**这条边界是按"信息可得性"切的，不是拍脑袋切的**。

**② `a2r.Call` 的统一封装**

153 个接口行为完全一致，不会有人忘写错误处理。**用抽象消灭了一整类 bug**。

**③ 限流放在认证之前**

便宜的检查靠前，避免被限流的请求白白消耗一次 gRPC。**细节体现性能意识**。

### 有问题的四处

**① `GinParseToken` 只处理 POST，无 default 分支**（`router.go:398-424`）

非 POST 请求绕过认证。目前只有 13 个内部 GET 接口，无实际影响，但**新增 GET 业务接口就是漏洞**。

**修法**：
```go
switch c.Request.Method {
case http.MethodPost:
    // ...
default:
    c.AbortWithStatus(http.StatusMethodNotAllowed)
}
```

**② `calculateBBRRetryAfter` 的 `loadRatio` 恒为 1**（`ratelimit.go:69`）

退避时间永远 1 秒。不影响限流是否生效，只影响重试提示的准确性。

**③ `api.Start` 有个未使用的 `service` 参数**（`init.go:29`）

签名欺骗性——让人以为 API 注册了 gRPC 服务。

**④ 管理员名单靠上游 ctx 传递**

API 层把 admin 名单塞进 ctx 传给 RPC。**下游不该信任上游传来的权限信息**——如果 API 层被攻破或出 bug，就能伪造管理员身份。

**更安全**：RPC 层自己从配置读名单，只信任上游传来的 userID。

---

# 第三部分 实验

## 实验 1：验证"200 不等于成功"

```bash
# 不带 token 调一个需要认证的接口
curl -i -X POST "http://127.0.0.1:10002/user/get_users_info" \
  -H "Content-Type: application/json" \
  -d '{"userIDs":["user001"]}'
```

**预期**：
```
HTTP/1.1 200 OK                     ← 状态码是 200
{"errCode":1501,"errMsg":"..."}     ← 但业务失败
```

**这验证了理论 5.7.3：不能只看状态码。**

## 实验 2：验证 GET 绕过认证（重要）

```bash
# 用 GET 访问一个只注册了 POST 的路径
curl -i "http://127.0.0.1:10002/user/get_users_info"
```

**预期**：返回 **404**（因为路由只注册了 POST）。

**但如果访问一个真的注册了 GET 的接口**：

```bash
curl -i "http://127.0.0.1:10002/prometheus_discovery/api"
```

**预期**：**不带 token 也能拿到数据**。

**这验证了 5.14.3 的隐患**：GET 路由完全绕过 token 校验。

⚠️ **请只在本地环境做这个实验**。生产环境这些接口应该被网络策略隔离。

## 实验 3：观察 BBR 响应头

```bash
curl -i -X POST "http://127.0.0.1:10002/auth/get_admin_token" \
  -H "Content-Type: application/json" \
  -d '{"secret":"<见凭据文件>","userID":"imAdmin"}'
```

**预期**：响应头里有
```
X-Bbr-Cpu: 12
X-Bbr-Minrt: 800
X-Bbr-Maxpass: 1000
X-Bbr-Maxinflight: 125
X-Bbr-Inflight: 1
```

**这些数字就是 BBR 的内部状态**：
- `Maxpass` 最大通过量；
- `Minrt` 最小响应时间；
- `Maxinflight` 算出来的最大并发；
- `Inflight` 当前并发。

**如果 `Inflight` 接近 `Maxinflight`，说明系统快满了。**

📌 这个实验能让你直观理解"自适应限流"在看什么指标。

## 实验 4：验证限流

```bash
# 快速打 1000 个请求
for i in $(seq 1 1000); do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -X POST "http://127.0.0.1:10002/auth/get_admin_token" \
    -H "Content-Type: application/json" \
    -d '{"secret":"x","userID":"y"}' &
done | sort | uniq -c
```

**预期**：大部分 200，一部分 429（取决于 CPU 阈值和机器性能）。

⚠️ 需要先在配置里开启 `rateLimiter.enable`。

---

# 第四部分 自测题

## 理论层

1. HTTP 请求由哪四部分组成？空行能省吗？
2. GET 和 POST 的幂等性差异，对"重试策略"有什么影响？
3. 4xx 和 5xx 的分界是什么？监控为什么只看 5xx？
4. REST 的三条核心原则是什么？
5. OpenIM 为什么不用 REST？四个理由。
6. 路由为什么用 Trie 而不是遍历？
7. 路由组有哪三个好处？
8. 中间件的"洋葱模型"是什么意思？靠什么实现？
9. 中间件不调 `Abort()` 会怎样？为什么？
10. 中间件顺序的三条原则？Logger 为什么要在最外层？
11. 认证和授权的区别？401 和 403 分别表示什么？
12. 为什么认证在 API 层、授权在 RPC 层？
13. 敏感数据为什么不能放 URL？四个泄露渠道。
14. 限流是保护谁？不限流会导致什么连锁反应？
15. 固定窗口的"临界问题"是什么？
16. 令牌桶和漏桶的区别？各适合什么场景？
17. BBR 和令牌桶的根本差异？它靠什么估算容量？
18. Little's Law 是什么？
19. 统一响应体的四个好处？代价是什么？
20. 参数校验分三层，各层负责什么？前端校验算安全措施吗？

## 代码层

21. `api.Start` 的 `service` 参数为什么没用？这说明什么？
22. 中间件链里，限流为什么在认证之前？
23. 只有哪个路由组挂了授权中间件？为什么只有它？
24. `GinParseToken` 只处理 POST 会导致什么？目前有实际影响吗？
25. 白名单机制的风险是什么？
26. `c.Set(OpUserID, ...)` 有什么类型安全问题？
27. `operationID` 怎么跨进程传递？gRPC 用什么载体？
28. `a2r.Call` 做了哪四件事？它的收益和代价？
29. `calculateBBRRetryAfter` 的 bug 是什么？影响范围多大？
30. `done(ratelimit.DoneInfo{})` 如果忘了调会怎样？

---

# 第五部分 面试话术

## 30 秒版

> "API 层是薄翻译层，2458 行里几乎没有业务逻辑。它做四件事：HTTP 与 gRPC 的协议转换、token 认证、参数绑定校验、统一响应。
>
> 认证放这里因为只有它能读 HTTP header；授权放在 RPC 层，因为授权需要业务数据，而 API 层不碰存储。这个分工是按'谁能拿到所需信息'切的。"

## 3 分钟版

> "我认为有三点值得说。
>
> **第一是分层边界。** 认证在 API、授权在 RPC，这不是随便切的。认证只需要 token，API 层能读 header 就能做；授权要判断'你是不是这个群的管理员'，必须有业务数据，而 API 层是薄层不碰数据库。所以边界画在'信息可得性'上。直接后果是 RPC 端口绝不能暴露公网——绕过 API 就没有认证了。
>
> **第二是中间件顺序。** 限流挂在认证之前，因为认证要调一次 auth 服务的 gRPC，被限流的请求没必要白花这次调用。这和 WebSocket 网关'先查连接数上限再鉴权'是同一个思路：越便宜的检查越靠前。
>
> **第三是我读代码发现的两个问题。** 一个是认证中间件只处理 POST 且没有 default 分支，按 Gin 的语义不调 Abort 后续 handler 仍会执行，所以非 POST 请求会绕过认证——目前只有 13 个内部 GET 接口所以没有实际影响，但如果新增 GET 业务接口就是越权漏洞。另一个是限流退避时间的计算把 loadRatio 写成了 CPU 除以 CPU，恒等于 1，导致两个阈值分支都是死代码。"

## 追问预判

| 可能被问 | 怎么答 |
|---|---|
| "为什么所有接口都是 POST？" | RPC 风格，路径即方法名。参数复杂必须放 body，而且要和 gRPC 方法一一对应。不是 RESTful，也不该硬说是。 |
| "200 但 errCode 非 0，监控怎么做？" | 按 errCode 聚合。项目里 prommetricsGin 就是取 errCode 而不是状态码。代价是 Nginx 层面看不出错误率。 |
| "BBR 和令牌桶选哪个？" | 有明确配额要求（计费）用令牌桶；负载波动大、不想手工调阈值用 BBR。BBR 靠 Little's Law 估容量，不需要预设 QPS。 |
| "单机限流够吗？" | 不够精确。10 个实例各限 1000，总量是 10000，但单用户被均匀打散后能拿到 10 倍配额。精确限流要 Redis 集中计数。 |
| "白名单安全吗？" | 白名单接口完全公开，必须确认不含敏感数据且有其他保护。比如 get_admin_token 靠 secret 兜底。加白名单应该是安全审计的重点。 |
| "operationID 和 traceID 区别？" | 本质一样，都是请求级唯一 ID。OpenTelemetry 的 traceID 还带 span 层级，能看每段耗时；项目里是自制的单层透传。 |

---

**下一篇**：[06 WebSocket 网关实现](06-WebSocket网关实现.md) —— 长连接的另一半：为什么服务器要主动说话，以及几十万连接怎么管。
