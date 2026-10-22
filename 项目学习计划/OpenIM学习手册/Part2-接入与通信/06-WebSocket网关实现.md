# 06 WebSocket 网关实现：从「服务器怎么主动找客户端」到「几十万连接怎么管」

> **本篇定位**：这是整套手册里第一篇真正的「技术深水区」。读完你应该能做到：给一个没接触过后端的人，从 HTTP 讲起，一路讲到 WebSocket 网关的连接管理，中途不被任何一个名词卡住。
>
> **阅读方式**：
> - **第一部分（6.1–6.8）纯理论**，一行源码都没有。看不懂代码也能读完，建议先完整读一遍，建立心智模型。
> - **第二部分（6.9–6.18）进入源码**，每个结论都对应 `文件:行号`。
> - **第三部分**是能亲手跑的实验，用来验证理论。
>
> **本篇涉及的名词**（先扫一眼，读不懂没关系，正文都会讲）：
> WebSocket、HTTP 短连接、轮询、长轮询、SSE、全双工、协议升级（Upgrade）、握手（Handshake）、帧（Frame）、掩码（Mask）、心跳（Heartbeat）、半开连接、Token、JWT、网关（Gateway）、连接表、粘性会话、多端互踢、goroutine、channel、背压（Backpressure）。

---

# 第一部分 理论：从零理解 WebSocket

## 6.1 先搞懂 HTTP：它的三个特征，和一个致命局限

不先讲 HTTP，就讲不清 WebSocket。因为 **WebSocket 是「寄生」在 HTTP 上的**——它靠一个 HTTP 请求完成"开场"，然后才脱离 HTTP。

### 6.1.1 HTTP 的本质：一次「提问—回答」

HTTP 的工作方式可以类比成**去柜台办事**：

```
你（客户端）：我要 /user/profile 这个资料        ← 请求 Request
柜台（服务器）：给你，这是结果                   ← 响应 Response
（办事结束，你离开柜台）
```

这条链路有三个铁打的特征：

**特征一：永远是客户端先开口。**
服务器不能主动找你。它甚至不知道你在哪——它只会在某个端口上"等着"，谁来了就服务谁。

**特征二：一问一答，答完就结束。**
一个 HTTP 请求对应一个响应。响应发完，这次交互就完结了。你下次再问，是**新的一次**交互，服务器不记得上次你是谁。

**特征三：服务器不记得你（无状态）。**
HTTP 协议本身不保存任何会话信息。你第一次请求登录接口，第二次请求拉好友列表，从协议角度看这是两个毫无关系的陌生人。

> 注意：HTTP 的无状态不是说"服务器不能做有状态的事"，而是说**协议层面不提供**。所以人们发明了 Cookie / Session / Token 这些"外挂"，让服务器自己把状态存起来。这一点在第 05 篇（HTTP API 层）和本篇的鉴权部分都会反复出现。

### 6.1.2 这个设计撑不住 IM

现在把场景换成聊天软件。假设你和小明在聊天：

- 小明发了一条消息给服务器；
- 服务器要把这条消息**立刻**送到你的手机上。

按 HTTP 的规则，服务器做不到。它只能在你想起来"我去刷新一下"的时候，把消息给你。

**这就是 IM 与普通 Web 应用的根本差异：**

| 场景 | 谁发起 | 数据流向 |
|---|---|---|
| 看网页、查资料 | 客户端 | 客户端拉，服务器给（**拉模式 Pull**） |
| **聊天、推送** | **服务器** | **服务器推，客户端收（推模式 Push）** |

HTTP 天生只支持"拉"。要让服务器"推"，就得想办法绕过去。绕的办法有四种，我们一个一个看。

---

## 6.2 服务器想主动说话：历史上的四条路

这四条路是理解 WebSocket 价值的必经之路。**不要跳过**——面试官问"为什么不用轮询"时，答案就在下面。

### 6.2.1 方案一：短轮询（Polling）——每隔几秒问一次

最朴素的想法：客户端定时发 HTTP 请求问"有新消息吗"。

```
0s   客户端：有新消息吗？  服务器：没有
3s   客户端：有新消息吗？  服务器：有 1 条
6s   客户端：有新消息吗？  服务器：没有
9s   客户端：有新消息吗？  服务器：没有
```

**问题一：延迟不可控。**
如果小明在 3.1 秒发消息，你要等到 6 秒才拿到——**平均延迟 = 轮询间隔 / 2**。想降低延迟就把间隔调小，但那样问题二更严重。

**问题二：绝大多数请求是浪费的。**
上图中 4 次请求有 3 次是空手而归。假设 100 万在线用户、3 秒轮询一次，就是**每秒 33 万次 HTTP 请求**，其中 99% 在回答"没有"。

**问题三：每次都要重新建立一次 HTTP 连接（或用 keep-alive 复用一个连接但反复发头）。**
HTTP 请求头通常几百字节到 1KB，真正有用的响应可能只有几十字节。**协议开销远大于数据本身**。

### 6.2.2 方案二：长轮询（Long Polling）——把请求"挂住"

改进思路：客户端发请求后，**服务器不立刻回答，而是把请求挂起（hold）**，等到真有消息了才返回。

```
0s   客户端：有新消息吗？  → 服务器：(挂住，不回答)
3s   小明发消息          → 服务器：有！给你   ← 立刻返回
3s   客户端：有新消息吗？  → 服务器：(继续挂住)
30s  什么都没有           → 服务器：没有（超时返回）
30s  客户端：有新消息吗？  → 服务器：(继续挂住)
```

**优点**：延迟低（消息一到就能返回）。
**缺点**：
- 服务器要为每个在线用户**保留一个挂起的请求**，本质上是"占着一个连接等"，资源消耗和长连接差不多，但**每次消息后还要重建请求**；
- 超时重连的瞬间会有一波请求洪峰（想象 100 万人同时超时重连）；
- 仍然是"一问一答"，无法双向同时说话。

长轮询在 WebSocket 普及前是主流（微信网页版早期、Comet 技术），现在基本被淘汰。

### 6.2.3 方案三：SSE（Server-Sent Events）——服务器单向广播

SSE 是 HTML5 标准的一部分，用普通 HTTP 实现"服务器持续下发文本"。

```
客户端：GET /events   Accept: text/event-stream
服务器：200 OK，Content-Type: text/event-stream
        data: 小明发来一条消息\n\n
        data: 小红上线了\n\n
        （连接一直不关，持续下发）
```

**特点**：
- 只支持**服务器 → 客户端**单向（客户端想说话还得另发 HTTP 请求）；
- 只能传 **UTF-8 文本**，二进制（图片/语音）要额外 base64，体积膨胀 33%；
- 浏览器原生支持，自动重连，实现极简单。

**适用**：股票行情、日志流、通知推送这类"只需下行"的场景。IM 需要双向（你要发消息、要发已读回执、要发正在输入），SSE 不够用。

### 6.2.4 方案四：WebSocket——真正的全双工

WebSocket 的目标很直接：**在一条 TCP 连接上，让双方随时都能主动发数据**。

```
客户端 ⇄═══════════ 一条 TCP 连接 ═══════════⇄ 服务器
        任何一方、任何时刻，都可以直接发一帧
```

**四个关键性质**：

| 性质 | 含义 | 对 IM 的意义 |
|---|---|---|
| **全双工（Full-duplex）** | 同一时刻双向都能发 | 你发消息的同时能收到别人的消息 |
| **长连接** | 建立一次，用几小时甚至几天 | 不用反复握手，没有重复的 HTTP 头开销 |
| **低开销** | 每帧只有 2~14 字节头 | 一条"在吗"可能只有 20 字节，而 HTTP 要几百字节 |
| **服务器可主动推** | 服务器持有连接引用，随时 write | 消息一到就能立刻送达 |

### 6.2.5 四条路对比（记住这张表）

| 方案 | 延迟 | 服务器资源 | 双向 | 协议开销 | 二进制 | 现状 |
|---|---|---|---|---|---|---|
| 短轮询 | 高（间隔/2） | 中（请求频繁但短） | ❌ | 高 | 可以 | 遗留系统 |
| 长轮询 | 低 | 高（挂起请求占连接） | ❌ | 中 | 可以 | 少量兼容场景 |
| SSE | 低 | 中 | 单向下行 | 低 | ❌（仅文本） | 行情/通知 |
| **WebSocket** | **低** | **高（连接常驻）** | **✅ 全双工** | **极低** | **✅** | **IM 标准方案** |

⚠️ **一个容易忽略的点**：WebSocket 的"服务器资源高"不是缺点描述，而是**代价**。一条连接要占一个 socket 文件描述符、一份读缓冲区、一个 goroutine。10 万连接就是 10 万个这些东西。所以后面 6.6、6.8 讲的**连接管理**才是网关真正的技术含量所在——**建立连接不难，管住几十万条连接才难**。

---

## 6.3 WebSocket 到底是什么：一次「协议升级」

### 6.3.1 最容易误解的一点

很多人以为 WebSocket 是"另一种和 HTTP 并列的协议"。

**准确说法是**：WebSocket 是**借用一次 HTTP 请求完成握手，然后在同一条 TCP 连接上切换到自己的帧协议**。

```
阶段一（还是 HTTP）：
  客户端 ──HTTP GET /ws + Upgrade 头──→ 服务器
  客户端 ←──HTTP 101 Switching Protocols── 服务器

阶段二（已经不是 HTTP 了）：
  客户端 ⇄──WebSocket 帧（二进制格式）──⇄ 服务器
```

**为什么要借 HTTP？** 因为现实世界里所有防火墙、代理、CDN 都放行 80/443 端口的 HTTP 流量。如果 WebSocket 自己搞一个全新协议、新端口，会被大量网络设备拦掉。**伪装成 HTTP 起步，是工程上的务实选择**。

### 6.3.2 握手：那几行 HTTP 头到底在干什么

客户端发出的请求长这样（这是 RFC 6455 规定的格式）：

```http
GET /msg_gateway?sendID=user001&token=xxx&platformID=5&isBackground=false HTTP/1.1
Host: im.example.com
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==
Sec-WebSocket-Version: 13
```

逐行解释：

| 头部 | 作用 | 人话 |
|---|---|---|
| `Upgrade: websocket` | 声明我想升级成 WebSocket | "我不是来取网页的，我想换个玩法" |
| `Connection: Upgrade` | 配合上一行，告诉中间层这是个升级请求 | "这个连接要变性了，别按 HTTP 缓存" |
| `Sec-WebSocket-Key` | 16 字节随机数的 base64 | 防缓存/防误连的随机挑战 |
| `Sec-WebSocket-Version: 13` | 协议版本 | 当前唯一的正式版本就是 13 |

服务器如果同意，返回：

```http
HTTP/1.1 101 Switching Protocols
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=
```

**三个必须理解的点**：

**① `101 Switching Protocols` 不是 200。**
101 的语义是"我同意切换协议"。从这个响应之后，**这条 TCP 连接上跑的不再是 HTTP**。任何 HTTP 代理如果不懂 101，就会在这里出问题（这也是为什么 Nginx 要专门配 `proxy_set_header Upgrade $http_upgrade`）。

**② `Sec-WebSocket-Accept` 不是随便算的。**
算法固定：把客户端的 `Sec-WebSocket-Key` 拼上一个魔法字符串 `258EAFA5-E914-47DA-95CA-C5AB0DC85B11`，做 SHA-1，再 base64。

```
accept = base64( SHA1( key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" ) )
```

**为什么要这么绕？** 不是加密，是**证明"我真的懂 WebSocket 协议"**。因为一个纯粹的 HTTP 服务器（或缓存）收到 Upgrade 请求时，只会把它当普通请求处理、返回 200 和资源内容，无法算出正确的 Accept 值。这样客户端就能确认对面不是"假装的"。

**③ 握手阶段可以带业务参数。**
看上面的 URL：`?sendID=user001&token=xxx&platformID=5&isBackground=false`。

**这是本项目鉴权的入口**。因为在握手这个时间点，双方还没进入 WebSocket 帧通信，服务器可以直接读 HTTP 请求的 URL 和 Header——**这是最后一次能"按 HTTP 的方式"拿到凭据的机会**。

### 6.3.3 帧（Frame）：握手之后的通信单位

握手完成后，传输的最小单位叫**帧**。每一帧有一个固定格式的头部（2~14 字节）+ 载荷。

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-------+-+-------------+-------------------------------+
|F|R|R|R| opcode|M| Payload len |    Extended payload length    |
|I|S|S|S|  (4)  |A|     (7)     |             (16/64)           |
|N|V|V|V|       |S|             |   (if payload len==126/127)   |
| |1|2|3|       |K|             |                               |
+-+-+-+-+-------+-+-------------+ - - - - - - - - - - - - - - - +
|     Extended payload length continued, if payload len == 127  |
+ - - - - - - - - - - - - - - - +-------------------------------+
|                               |Masking-key, if MASK set to 1  |
+-------------------------------+-------------------------------+
| Masking-key (continued)       |          Payload Data         |
+-------------------------------- - - - - - - - - - - - - - - - +
```

不用背这个图，只需记住四个字段：

| 字段 | 作用 | 为什么重要 |
|---|---|---|
| **opcode** | 帧类型 | **决定这帧是文本、二进制还是 ping/pong** |
| **MASK** | 是否掩码 | 客户端发出的帧必须置 1，服务器发出的必须置 0 |
| **Payload len** | 载荷长度 | 7 位直接表示（≤125），126/127 表示用后续字节表示更长 |
| **Masking-key** | 4 字节掩码 | 客户端用它异或载荷 |

**opcode 的取值（必须记住前几个）**：

| opcode | 名称 | 含义 |
|---|---|---|
| `0x0` | Continuation | 分片的后续帧 |
| `0x1` | **Text** | 文本帧（UTF-8） |
| `0x2` | **Binary** | 二进制帧 |
| `0x8` | Close | 关闭连接 |
| `0x9` | **Ping** | 心跳探测 |
| `0xA` | **Pong** | 心跳应答 |

### 6.3.4 为什么客户端必须掩码

RFC 6455 规定：**客户端发往服务器的帧必须用 4 字节随机 key 掩码，服务器发往客户端的帧禁止掩码**。

掩码算法很简单：`payload[i] ^= key[i % 4]`。

**目的不是加密，是防"缓存投毒"**：早期有攻击者利用浏览器可以发任意字节的特点，构造出看起来像 HTTP 请求的 WebSocket 载荷，欺骗中间的代理缓存。强制客户端掩码后，攻击者就无法控制线上字节的精确内容（因为 key 是随机的），从而保护了中间设施。

⚠️ **这条规则解释了你在调试时的"坑 2"**：
> `not support text message type`

因为项目的读循环**只处理 binary 帧**（`opcode=0x2`），所以用工具发文本帧会被直接拒绝。这不是 bug，是**刻意收窄协议**：整个业务协议都跑在二进制帧里，收到文本帧说明客户端用错了 SDK。

### 6.3.5 一条连接的完整生命周期

把上面串起来，一条 WebSocket 连接的一生是这样的：

```
① TCP 三次握手            （传输层连接建立）
        ↓
② HTTP Upgrade 请求       （客户端说：我想升级）
        ↓
③ 服务器鉴权 / 限流 / 校验  （本项目在这一步调 auth 服务验 token）
        ↓
④ 101 Switching Protocols （协议切换，HTTP 阶段结束）
        ↓
⑤ 双向帧通信              （收发业务消息 + 心跳 ping/pong）
        ↓
⑥ Close 帧 / TCP 断开      （任一方主动关闭，或超时）
```

**请注意第 ③ 步在 ④ 之前**。这个顺序非常关键，6.4 会专门讲为什么。

---

## 6.4 握手鉴权：为什么要在"升级之前"验身份

### 6.4.1 先搞懂"鉴权"这个词

**认证（Authentication）** 和 **授权（Authorization）** 经常被混用，这里先分清：

| 概念 | 回答的问题 | 例子 |
|---|---|---|
| **认证** | 你是谁？ | 验证 token → 得到 userID |
| **授权** | 你能干什么？ | 你是不是这个群的管理员？ |

握手阶段做的是**认证**：确认"这个连接声称自己是 user001，他真的是吗"。

### 6.4.2 从 Session 讲到 Token

要理解 token 是什么，得先知道它替代了什么。

**方案一：Session（服务器记）**

```
① 用户登录，服务器创建一个 session 对象存在内存/Redis 里
   session = { id: "abc123", userID: "user001", expire: ... }
② 服务器把 sessionID 通过 Cookie 发给浏览器
③ 之后每次请求，浏览器自动带上 Cookie: sessionID=abc123
④ 服务器拿 sessionID 去 Redis 查：哦，是 user001
```

问题：**服务器必须保存状态**。多台服务器要么共享 Redis（多一次查询），要么做会话粘滞（把同一用户固定打到同一台机器）。

**方案二：Token（客户端记，服务器验签）**

```
① 用户登录，服务器签发一个字符串（token）：
   token = base64(header) + "." + base64(payload) + "." + 签名
                                    ↑ 里面写着 userID、过期时间
② 客户端保存，之后每次请求放在 Header: Authorization: Bearer <token>
③ 服务器拿到 token，用密钥验签 + 检查过期时间，就能得出 userID
   —— 不需要查任何存储
```

这就是 **JWT（JSON Web Token）** 的思路。它的核心价值：**服务器无状态**。任意一台服务器拿到 token 都能独立验证，不需要共享会话。

一个 JWT 拆开长这样：

```
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9   ← header（算法）
.
eyJ1c2VySUQiOiJ1c2VyMDAxIiwiZXhwIjoxNz...}  ← payload（userID、过期时间）
.
dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk  ← signature（签名）
```

解码 payload 就能看到业务信息——**注意：payload 只是 base64，不是加密，任何人都能读**。安全性完全依赖签名：改了 payload 就验不过签。

⚠️ **易错**：因此 token 里**绝不能放密码等敏感信息**。

### 6.4.3 三个鉴权时机，各自的代价

WebSocket 建连后，理论上你有三个地方可以验身份：

| 时机 | 做法 | 优点 | 缺点 |
|---|---|---|---|
| **A. 握手前**（HTTP 阶段） | 从 URL/Header 取 token，验完再升级 | 未授权连接**根本建立不起来**，零资源浪费 | 参数必须放 URL（浏览器 WS API 不支持自定义 Header） |
| B. 升级后首包 | 先升级，再等客户端发一条"登录包" | 参数可放 body | 未授权连接已占用 socket 和 goroutine，可被用来打资源 |
| C. 每条消息都验 | 每次请求带 token 重验 | 支持 token 中途失效 | 每条消息多一次开销 |

**本项目的选择是 A + C 混合**：
- 握手时验（A），不合格直接返回 HTTP 错误，连不上；
- 每条业务消息的 `Req` 里还带 `token` 字段（C 的弱化版），用于检测 token 过期。

**为什么不做 B？** 因为 `B` 相当于"先让你进门，再问你有没有票"。攻击者可以疯狂建立连接、一个登录包都不发，把网关的 goroutine 和 socket 资源吃光。**把鉴权放在升级前，是最省资源的做法**——这也是"防御性设计"的一个典型例子。

### 6.4.4 校验失败要返回什么

这里有个容易踩的细节：**失败发生在哪个阶段，决定你能用什么方式报错**。

```
如果失败在 ④ 之前 → 你还能返回 HTTP 响应（400/401/403 + JSON 错误体）
如果失败在 ④ 之后 → 你只能发 WebSocket 帧（Resp{errCode: ...}），或直接关连接
```

本项目的 `handlerError`（`ws_server.go`）就是在"还没升级"的阶段把错误通过 HTTP 写回去，客户端拿到的是普通的 HTTP 错误响应，而不是 WS 帧。

🧠 **想清楚这个顺序**，你就明白为什么"握手相关的坑"报错信息看起来不像 WebSocket 的错误——因为它们本质上是 HTTP 层的错误。

---

## 6.5 连接会自己"死"：心跳与半开连接

### 6.5.1 一个反直觉的事实：TCP 连接可能"假活"

TCP 是面向连接的，理论上断开会通知对端（FIN/RST）。但现实中大量连接**不会正常断开**：

| 场景 | 发生了什么 |
|---|---|
| 手机进电梯、地铁 | 信号突然消失，没有任何 FIN 包，对端以为你还活着 |
| 手机锁屏 / App 被系统冻结 | 进程被挂起，socket 还在，但没人读数据 |
| NAT 网关表项超时 | 家用路由器为了省资源，几分钟没流量就把映射删了，连接名存实亡 |
| 负载均衡 idle timeout | Nginx/ELB 默认 60s 无数据就断，但两端可能都不知道 |
| 服务器进程被 kill -9 | 内核会发 RST，这种反而能感知 |

这种"一端已死、另一端以为活着"的连接叫**半开连接（Half-open Connection）**。

**后果很严重**：
- 服务器以为用户在线，把消息推过去 → 写进一个已经没人读的 socket → 消息丢了，但服务器认为推送成功；
- 连接表里堆积大量僵尸连接 → 内存泄漏、连接数虚高，最后拒绝新连接。

### 6.5.2 为什么 TCP 自带的 keepalive 不够

TCP 有一个 `SO_KEEPALIVE` 选项，操作系统会定期发探测包。但它有三个问题：

1. **默认时间太长**：Linux 默认 2 小时才发第一次探测。IM 场景完全不能接受。
2. **只能探测"TCP 层通不通"**：如果对端主机活着、TCP 栈正常，但**应用进程卡死/被冻结**，TCP keepalive 依然认为连接健康。
3. **穿过代理时不可靠**：中间设备可能各自维护状态。

所以**应用层要自己做心跳**：双方约定"我每隔 N 秒给你发一个信号，你必须在 M 秒内回一个，否则我就认为你死了"。

### 6.5.3 WebSocket 的心跳：ping / pong 控制帧

WebSocket 协议**内置**了心跳帧：

```
服务器 ──Ping(opcode=0x9)──→ 客户端
服务器 ←──Pong(opcode=0xA)── 客户端
```

这两个是**控制帧**，不占用业务数据的通道，业务层感知不到。

**为什么不用业务消息做心跳？** 因为控制帧开销极小（2 字节头 + 少量载荷），而且协议栈直接处理，不会进入业务逻辑。

### 6.5.4 三个时间参数的关系（这是设计重点）

本项目在 `internal/msggateway/constant.go:46-58` 定义了三个常量：

```go
writeWait      = 10 * time.Second    // 单次写操作的超时
pongWait       = 30 * time.Second    // 等待 pong 的最长时间
pingPeriod     = (pongWait * 9) / 10 // = 27s，必须小于 pongWait
maxMessageSize = 51200               // 50KB，单帧上限
```

**为什么 `pingPeriod` 必须严格小于 `pongWait`？**

想象你在等一个"30 秒内必须回"的电话。如果你在第 30 秒才打出去，对方哪怕立刻接，也可能因为网络抖动超过 30 秒——你会误判对方死了。

留 10% 的余量（27s vs 30s）是为了**吸收网络抖动**：27 秒发 ping，即使响应慢了 2 秒，也还在 30 秒的窗口内。

**如果 `pingPeriod >= pongWait` 会怎样？** 每次 ping 发出后，还没等到 pong 就超时断开 → **连接疯狂闪断**。这是个很容易写错的地方。

### 6.5.5 谁发 ping，谁发 pong

标准做法是**服务器主动 ping，客户端自动 pong**。但本项目的实现有个细节：

```go
// ws_server.go:538-541
var pingInterval time.Duration
if connContext.GetPlatformID() == constant.WebPlatformID {
    pingInterval = pingPeriod
}
```

**只有 Web 平台（浏览器）才由服务端主动 ping**，其他平台 `pingInterval = 0`（不 ping）。为什么？

因为**移动端 SDK 自己有心跳机制**：
- iOS/Android 的 App 会被系统挂起，服务端 ping 过去根本收不到，反而会误判离线；
- 移动端 SDK 更清楚自己什么时候是"真在线"，所以由 SDK 自己发**业务层心跳**（走 `TextPing`/`TextPong`，见 `message_handler.go:19-22`）。

而浏览器里没有这种系统级休眠，服务端 ping 最可靠，所以由服务端来。

📌 **这个细节很有面试价值**：它说明"心跳策略要跟着客户端形态走"，不是一套参数打天下。

---

## 6.6 「网关」到底是个什么东西

### 6.6.1 一句话定义

> **网关（Gateway）= 专门负责"接住长连接"的那一层服务。**

它和业务服务（用户、好友、群组、消息）是**两种完全不同性质的进程**：

| | 业务服务（RPC） | 网关（Gateway） |
|---|---|---|
| 是否有状态 | **无状态**（请求带够信息就能处理） | **有状态**（内存里存着几十万条连接） |
| 能否随意扩缩容 | 能，加机器就行 | 能扩，但**连接不会自动迁移** |
| 挂掉的影响 | 部分请求失败，重试即可 | **该节点上所有连接同时断开** |
| 主要压力 | CPU（业务计算）、DB | **内存 + 文件描述符 + 网络 IO** |

**这解释了架构上最重要的一条分界线**（第 03 篇讲过，这里是它的根因）：

```
客户端 ──WS──→ 网关（有状态）──gRPC──→ 业务服务（无状态）
```

如果让业务服务直接持有 WebSocket 连接，会发生什么？
- 用户服务重启 → 所有用户的连接全断 → **全站掉线**；
- 想扩缩容用户服务 → 连接分布不均；
- 业务代码和网络代码耦合，改一个业务逻辑要担心连接状态。

**所以：把"有状态"的部分单独抽出来，让它薄、让它稳、让它出问题只影响连接不影响数据。**

### 6.6.2 网关要做的事，只有四件

```
① 接住连接（握手、鉴权、限流）
② 编解码（把字节变成结构体，把结构体变成字节）
③ 维护连接表（谁在线、在哪个连接上、用什么平台）
④ 转发（客户端请求 → 转给 RPC；RPC 要推送 → 找到连接写出去）
```

**它不碰数据库，不判业务规则。** 你在网关代码里搜不到一条 Mongo 查询——这是刻意设计的。

### 6.6.3 连接表：网关的心脏

网关要回答一个问题：**"user001 现在在哪些连接上？"**

最简单的答案是一个 map：

```go
map[userID] → [连接1, 连接2, 连接3...]
```

但真实需求更复杂：**同一个人可能有多个设备**（手机、PC、iPad）。所以需要二级结构：

```go
map[userID] → map[platformID] → [连接...]
```

**为什么要区分平台？** 因为推送策略不一样：
- 手机在线，PC 也在线 → 两边都要推；
- 手机在后台（`isBackground=true`）→ 不能推（iOS 会挂起），要走离线推送；
- 想踢掉"手机端"登录，不能把 PC 端也踢了。

所以连接表必须**按平台分桶**。

### 6.6.4 多节点带来的两个难题

单机网关很简单。一旦部署多台（比如 10 台，每台 5 万连接），两个问题立刻出现：

**难题一：要推消息给 user001，他在哪台机器上？**

三种解法：

| 解法 | 做法 | 代价 |
|---|---|---|
| **广播** | 向所有网关节点发"你那儿有 user001 吗" | 节点数 × 消息量，浪费 |
| **路由表** | 用 Redis 记录 userID → 节点ID | 多一次查询，要维护一致性 |
| **RPC 直连** | 消息服务调网关的 gRPC 接口，网关自己查本地连接表 | 需要知道调哪台 |

本项目用的是**第三种**：push 服务通过服务发现拿到**所有网关节点**，然后向每个节点发 gRPC 推送请求，网关节点自己查本地连接表，有就推、没有就返回"不在我这"。

```go
// 简化示意：push 侧对每个网关节点发起 RPC
for _, node := range allGatewayNodes {
    node.PushToUser(ctx, &PushReq{UserID: "user001", Msg: msg})
}
```

📌 **代价**：如果部署 10 个网关、给 1 个人发消息，会产生 10 次 RPC，其中 9 次都是"不在我这"。这是**用带宽换架构简单**。更优的方案是维护路由表，但复杂度和一致性成本高。**这是一个明确的工程取舍**（第 03 篇的"推送怎么找到人"）。

**难题二：用户从 A 节点重连到 B 节点，A 还不知道。**

这就是 6.7 要多端互踢时要处理的**状态收敛延迟**问题。

### 6.6.5 为什么需要"粘性会话"

如果你的网关前面有一层负载均衡（Nginx/LVS），要注意：**WebSocket 是长连接，一旦建立就不能中途换后端**。

所以 LB 必须支持：
- `ip_hash` 或一致性哈希（同一客户端总打到同一节点）；
- 或者至少保证**连接建立后不会重新路由**（四层 LB 天然满足）。

HTTP 场景下"每个请求随便打哪台"的轮询策略，在 WebSocket 场景会直接失效。这是运维层面的常识，但很多人第一次部署时会踩。

---

## 6.7 多端登录：一个产品需求如何变成并发代码

### 6.7.1 先讲产品语义

"多端登录"听起来简单，实际是一组策略问题：

| 问题 | 可能的答案 |
|---|---|
| 同一个账号能同时登录几个设备？ | 不限 / 每种平台一个 / 只能一个 |
| 手机和 PC 能同时在线吗？ | 能（大多数 IM 都允许） |
| 两台手机能同时在线吗？ | 通常不允许（新的踢旧的） |
| 被踢的那端怎么通知？ | 静默断开 / 弹"您的账号在别处登录" |
| 被踢的 token 还能用吗？ | 不能（要失效） |

**这些不是技术选择，是产品选择**。所以代码里必然是**配置驱动 + 策略模式**。

### 6.7.2 四种策略的直觉

| 策略 | 人话 | 场景 |
|---|---|---|
| 都不踢 | 爱登几个登几个 | 内部测试 |
| PC 与其他 | PC 一个 + 移动端一个 | 常见默认 |
| 同端踢 | 同平台只留最新一个 | 最常见 |
| 同类踢 | 移动端算一类，PC 算一类 | 手机+平板算同类 |

### 6.7.3 难点：分布式下的"我以为他下线了"

设想这个时序（**这是本篇最值得理解的一段**）：

```
t0  用户从网关 A 断网（手机信号差），A 还不知道，连接还在
t1  用户立刻重连，连到了网关 B（LB 换了节点）
t2  B 收到新连接，按策略应该"踢掉旧的同平台连接"
t3  B 只知道自己节点上的连接，它看不到 A 上的旧连接
t4  B 通过 gRPC 通知 A："你那儿有个 user001 的手机连接，踢了"
t5  A 处理这条通知需要时间（网络 + 调度）
t6  在这段时间里，user001 有两条手机连接同时在线
```

**如果不做特殊处理，会发生什么？**

用户在 t1 重连成功后，t2~t5 期间旧连接还在。如果此时客户端收到"你被踢了"的通知——**用户会看到自己刚连上就被踢**，表现为"反复掉线"。

**本项目的解法：同 token 不踢。**

```go
// ws_server.go:353-355 附近的逻辑（简化）
if c.token == newClient.token {
    continue   // 同一个 token 的连接不踢
}
```

**为什么有效？** 因为用户重连时用的还是**同一个 token**（token 没变），所以 A 上的旧连接和 B 上的新连接 token 相同 → 判定为"这是同一个人重连的残留"，不踢。

**这个细节的价值**：它证明作者想过**分布式系统的状态收敛问题**——不是"加锁就能解决"，而是"有些状态天然有延迟，必须在业务规则上容忍它"。

📌 **面试时这样讲**：
> "多端互踢在多节点部署下有个坑：客户端从 A 断开重连到 B，A 的旧连接还没清理，会出现同一个 token 的两条连接。如果直接按策略踢，用户会把自己踢下线，表现为反复掉线。项目里的处理是同 token 不踢，本质是承认分布式状态有收敛延迟，在业务规则上做兼容。"

---

## 6.8 支撑几十万连接的并发原语

进入源码前，必须补齐 Go 并发的基础概念。这一节讲"为什么 Go 适合写网关"。

### 6.8.1 进程、线程、协程、goroutine

| 概念 | 是什么 | 切换成本 | 默认栈大小 |
|---|---|---|---|
| 进程 | 独立的地址空间 | 极高（涉及页表切换） | — |
| 线程（OS Thread） | CPU 调度的基本单位 | 高（微秒级，陷入内核） | 1~8 MB |
| 协程（Coroutine） | 用户态调度的执行单元 | 低 | 几 KB |
| **goroutine** | Go 运行时管理的协程 | **极低（百纳秒级）** | **2 KB 起，可增长** |

**关键数字对比**：

```
10 万条连接，每条需要一个执行单元阻塞等待数据：

用线程：100,000 × 1MB 栈 = 100 GB    ← 直接爆内存
用 goroutine：100,000 × 2KB = 200 MB ← 完全可行
```

**这就是 Go 写 IM 网关的核心优势**：不是语法简单，而是**"一连接一 goroutine"这个模型在资源上真的成立**。C++ 要用 epoll + 事件回调（异步非阻塞，代码极其复杂），Java 要用 NIO/Netty（同样复杂）。Go 用同步阻塞的写法就能达到同样效果，因为调度器在背后把阻塞的 goroutine 挂起、让出线程。

⚠️ **注意**：goroutine 便宜不等于免费。
- 每个 goroutine 至少 2KB 栈 + 运行时结构；
- goroutine 泄漏（只创建不退出）会缓慢吃光内存；
- 调度器在几十万 goroutine 时也有开销（Go 1.14+ 的异步抢占改善了这点）。

**所以网关必须控制 goroutine 数量**——这就是 6.8.4 的"背压"。

### 6.8.2 阻塞读：为什么一个 goroutine 只干一件事

```go
for {
    message, err := conn.ReadMessage()   // 这里会"卡住"，直到有数据
    handle(message)
}
```

`ReadMessage()` 是**阻塞调用**：没有数据时它会一直等。

看起来"一个 goroutine 被浪费在等待上"，但实际上：
- 这个 goroutine 被 Go 运行时**挂起**，不占 CPU；
- 它占的只是一个栈（2KB）和少量元数据；
- 数据一到，运行时把它唤醒继续跑。

**这是"用内存换代码简洁"**：你写的是同步代码（好懂、好调），底层是异步 IO（高效）。

### 6.8.3 channel 串行化 vs mutex 加锁

这是本篇的核心设计抉择，先讲原理。

**方案 A：加锁改 map**

```go
var mu sync.Mutex
var clients = map[string]*Client{}

func register(c *Client) {
    mu.Lock()
    defer mu.Unlock()
    old := clients[c.UserID]
    if old != nil && shouldKick(old, c) {
        old.Close()          // ← 关连接可能很慢
    }
    clients[c.UserID] = c
}
```

**问题**：
- `old.Close()` 可能涉及网络 IO，**在锁里做慢操作**会阻塞所有其他注册；
- 如果 `Close()` 的回调里又要拿锁 → **死锁**；
- 多个步骤（查旧连接 → 判断 → 关旧 → 存新 → 更新计数）无法原子完成，中间状态可能被其他 goroutine 看到。

**方案 B：channel 串行化（actor 模型）**

```go
registerChan := make(chan *Client, 1000)

go func() {
    for {
        select {
        case c := <-registerChan:
            doRegister(c)        // 只有这一个 goroutine 在改 map
        case c := <-unregisterChan:
            doUnregister(c)
        }
    }
}()
```

**所有修改都排进一个队列，由一个 goroutine 顺序处理**。由于只有它在改，**天然没有竞态，也不需要锁**。

**代价**：吞吐量受限于单 goroutine 的处理速度；队列满了会阻塞生产者。

**为什么这里选 B？** 因为注册/注销逻辑有**多步**（查旧连接、判断策略、踢人、跨节点广播、更新计数），用锁很难保证这整段的一致性，而串行化"免费"获得了原子性。

📌 **这是"用架构消除并发问题"的典型手法**，比"小心地加锁"更可靠。

### 6.8.4 背压（Backpressure）：队列满了怎么办

**背压**这个词来自水管：下游排得慢，上游就不能灌得太快，否则会爆。

在网关里，背压出现在很多地方：

| 场景 | 没有背压会怎样 | 背压手段 |
|---|---|---|
| 群发消息给 500 人 | 瞬间 500 个 goroutine 写 socket，IO 打满 | 有界队列 + 固定 worker 数 |
| 连接注册 | 10 万连接涌入，注册逻辑被压垮 | channel 缓冲 + 阻塞生产者 |
| 消息落库 | Kafka 堆积，内存爆 | 消费者限速 |

**核心公式**：

```
并发数 = min(队列容量, worker 数)
```

**没有背压的系统，在流量高峰时不是"变慢"，而是"崩溃"**（OOM、fd 耗尽）。这是 SRE 层面的常识，也是面试高频题。

### 6.8.5 对象池（sync.Pool）：为什么需要它

```go
var binaryReq = getReq()          // 从池里拿一个空结构体
defer freeReq(binaryReq)          // 用完放回去
```

**问题背景**：每条消息都要解析成一个 `Req` 结构体。如果每条消息都 `new(Req)`，10 万 QPS 就是每秒 10 万个对象 → **GC 压力巨大**（Go 的 GC 虽然优秀，但频繁分配会造成 STW 抖动和 CPU 浪费）。

`sync.Pool` 是 Go 提供的**临时对象池**：
- `Get()` 拿一个（没有就 `New()` 一个）；
- `Put()` 放回去；
- GC 时池里的对象**可能被清空**（这是设计，不是 bug，防止内存膨胀）。

⚠️ **易错**：`sync.Pool` 不保证 Put 进去的能 Get 出来。它只适合"减少分配"，不能当缓存用。

### 6.8.6 原子操作（atomic）：为什么不加锁

```go
var onlineNum atomic.Int64
onlineNum.Add(1)              // 原子加
n := onlineNum.Load()         // 原子读
```

对于**单个数值**的读写，用 `sync/atomic` 比 `sync.Mutex` 快得多（底层是 CPU 的 CAS 指令，无内核态切换）。

**但要注意限制**：
- 只能保证**单个操作**原子，多个操作组合（先读再写）**不原子**；
- 不要用 atomic 保护复杂结构（map 不行）。

**判断标准**：
- 只是计数/标志位 → `atomic`
- 需要"读-改-写"或多字段一致 → `mutex` 或 channel

### 6.8.7 errgroup：并发的错误与限流

```go
g := errgroup.Group{}
g.SetLimit(3)                     // 最多同时 3 个
for _, node := range nodes {
    g.Go(func() error {
        return callNode(node)
    })
}
g.Wait()                          // 等全部完成，返回第一个错误
```

`errgroup` 解决三个问题：
1. **并发执行**多个任务；
2. **收集错误**（`Wait()` 返回第一个非 nil 错误）；
3. **限制并发**（`SetLimit`）。

比手写 `sync.WaitGroup` + channel 收集错误简洁得多，而且 `SetLimit` 天然提供背压。

### 6.8.8 理论部分小结

现在你应该建立了这个心智模型：

```
IM 需要服务器主动推
    ↓
HTTP 做不到 → 用 WebSocket（借 HTTP 握手，然后全双工帧通信）
    ↓
握手前必须鉴权（省资源），所以 token 放在 URL 里
    ↓
长连接会被 NAT/系统冻结搞成"半开"，所以要有应用层心跳
    ↓
连接是"有状态"的，所以网关必须独立于业务服务
    ↓
多节点 → 推送要找连接、踢人要跨节点 → 状态收敛有延迟
    ↓
几十万连接 → 一连接一 goroutine（Go 的优势）+ 串行化 + 背压
```

**第二部分开始，看 OpenIM 怎么把这些变成代码。**

---

# 第二部分 源码：OpenIM 网关的实现

## 6.9 网关在系统中的位置

先把位置钉死。网关在 `internal/msggateway/`，是一个**独立的进程**（`cmd/openim-msggateway`），不和 API 混在一起。

```
                        ┌──────────────────────────────┐
   客户端 ──WebSocket──→ │ msggateway（本篇主角）        │
                        │  · 握手鉴权                   │
                        │  · 连接表（userID→连接）       │
                        │  · 编解码                     │
                        └───────┬──────────────▲───────┘
                                │ gRPC         │ gRPC（推送回调）
                                ↓              │
                        ┌───────────────┐  ┌───┴────────┐
                        │  msg RPC      │  │ push 服务   │
                        │（发消息/拉历史）│  │（在线/离线） │
                        └───────────────┘  └────────────┘
```

**注意箭头方向**：网关既**主动调** msg RPC（用户发消息），又被 push 服务**反向调用**（推消息给用户）。

这个"双向调用"是网关的特殊之处：它既是客户端（gRPC client），又是服务端（gRPC server）。所以 `WsServer` 里既有 `authClient` / `userClient`，又实现了 `LongConnServer` 接口给 push 服务调。

### 目录结构（本篇会读到的文件）

```
internal/msggateway/
├── ws_server.go        ← 服务主体：握手、事件循环、踢人（本篇核心）
├── context.go          ← 连接上下文：URL 参数解析
├── client.go           ← 单连接：读循环、分发、写帧
├── client_conn.go      ← 连接抽象：包装 gorilla/websocket
├── message_handler.go  ← Req/Resp 结构与 gRPC 转发
├── hub_server.go       ← 推送入口（被 push 服务调用）
├── user_map.go         ← 连接表
├── encoder.go          ← Gob / JSON 双编码器
├── compressor.go       ← gzip 压缩 + 对象池
├── subscription.go     ← 在线状态订阅
└── constant.go         ← 常量（指令码、超时）
```

### 启动：`NewWsServer` 与 `Run`

```go
// ws_server.go:143-146  —— 对应理论 6.3.2 的 Upgrader
upgrader := &websocket.Upgrader{
    HandshakeTimeout: config.handshakeTimeout,
    CheckOrigin:      func(r *http.Request) bool { return true },
}
```

**两个参数值得说**：

**① `HandshakeTimeout`**：握手必须在限定时间内完成，防止"只建 TCP 不发 Upgrade"的慢速攻击。

**② `CheckOrigin: return true`**：允许**任意来源**的跨域 WebSocket 连接。

⚠️ **这是一个安全上的取舍，必须理解**：
- 浏览器的同源策略默认禁止跨域 WebSocket，`CheckOrigin` 是服务端的把关点；
- 项目直接返回 `true`，意味着**任何网站都能连你的 WS 网关**；
- 安全性完全依赖 **token 鉴权**——没有合法 token，连上也发不了任何消息。

**为什么敢这么做？** 因为 IM 网关通常要服务多端（Web、小程序、App、第三方集成），来源域名不可枚举，白名单维护成本高。**用 token 做唯一防线**是合理的，但前提是 token 必须够强（HTTPS + 短过期 + 可吊销）。

📌 面试如果被问"WS 有没有 CSRF 风险"：**能答出"这里 CheckOrigin 全放行，靠 token 兜底"就是加分项**，因为这说明你真读了代码而不是背概念。

```go
// ws_server.go:171-188（简化）
func (ws *WsServer) Run(ctx context.Context) error {
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case client := <-ws.registerChan:
                ws.registerClient(client)          // 建立连接
            case client := <-ws.unregisterChan:
                ws.unregisterClient(client)        // 断开连接
            case onlineInfo := <-ws.kickHandlerChan:
                ws.multiTerminalLoginChecker(onlineInfo)  // 踢人
            }
        }
    }()
    // 另起 goroutine 跑 http.Server
}
```

**这段代码就是理论 6.8.3 的"channel 串行化"**：三个 channel 对应三类事件，一个 goroutine 顺序消费。

**注意 `select` 的语义**：多个 channel 同时有数据时，Go 会**随机选一个**。这里不需要严格顺序（注册和注销谁先谁后都行），但**同一个 channel 内部是 FIFO**——所以同一用户的两次注册不会乱序。

---

## 6.10 数据结构：WsServer 与 Client

### WsServer：全局唯一

```go
// ws_server.go:48-74
type WsServer struct {
    websocket         *websocket.Upgrader
    msgGatewayConfig  *Config
    port              int
    wsMaxConnNum      int64
    registerChan      chan *Client        // 缓冲 1000
    unregisterChan    chan *Client        // 缓冲 1000
    kickHandlerChan   chan *kickHandler   // 缓冲 1000
    clients           UserMap             // ← 连接表
    online            rpccache.OnlineCache // ← 在线状态（Redis）
    subscription      *Subscription        // ← 在线状态订阅
    clientPool        sync.Pool
    onlineUserNum     atomic.Int64         // ← 在线用户数
    onlineUserConnNum atomic.Int64         // ← 在线连接数
    handshakeTimeout  time.Duration
    writeBufferSize   int
    validate          *validator.Validate
    disCov            discovery.Conn
    Compressor
    MessageHandler
    webhookClient *webhook.Client
    userClient    *rpcli.UserClient
    authClient    *rpcli.AuthClient
    ready atomic.Bool
}
```

**字段分四组看**：

| 组 | 字段 | 作用 |
|---|---|---|
| **配置** | `wsMaxConnNum`、`handshakeTimeout`、`writeBufferSize` | 上限与超时 |
| **并发结构** | 三个 channel、两个 atomic、clientPool | 事件循环与计数 |
| **状态** | `clients`（连接表）、`online`（Redis 在线状态）、`subscription` | 谁在线 |
| **依赖** | `authClient`、`userClient`、`MessageHandler`、`disCov`、`webhookClient` | 调别的服务 |

⚠️ **注意 `onlineUserNum` 和 `onlineUserConnNum` 是两个不同的数**：
- `onlineUserNum`：**用户数**（user001 有三个设备，只算 1）；
- `onlineUserConnNum`：**连接数**（算 3）。

**为什么要分开？** 因为：
- 连接数用于**限流**（保护自己不被压垮）；
- 用户数用于**运营统计/在线状态**（业务关心"多少人在线"）。

如果只有一个数，要么限流失准（一人多设备时低估资源），要么业务数据错误。

### Client：一根连接

```go
// client.go
type Client struct {
    ctx            *UserConnContext   // 连接上下文（userID/platform/connID）
    conn           LongConn           // 连接抽象（包装 gorilla/websocket）
    Encoder        Encoder            // Gob 或 JSON
    PlatformID     int
    UserID         string
    token          string
    IsBackground   bool               // App 是否在后台
    IsCompress     bool
    closed         atomic.Bool        // ← 无锁关闭标志
    closedErr      error
    w              sync.Mutex         // ← 写锁
    longConnServer LongConnServer     // 反向引用（回调网关）
    hbCtx          context.Context    // 心跳 ctx
    hbCancel       context.CancelFunc
    subUserIDs     map[string]struct{} // 订阅了谁
    subLock        sync.Mutex
    SDKType        string
    SDKVersion     string
}
```

**三个设计点**：

**① `longConnServer LongConnServer` 是反向引用**

Client 需要回调网关的方法（发消息、拉历史、踢人）。如果直接持有 `*WsServer` 会造成循环依赖（WsServer 有 map[*Client]，Client 有 *WsServer）。用**接口**打破了这个环，也方便测试时 mock。

**② `closed atomic.Bool` 而不是 `bool`**

因为 `closed` 会被多个 goroutine 读写（读协程检查、事件循环设置、心跳协程检查）。普通 bool 会有**数据竞争**（race），必须用 atomic。

**③ `w sync.Mutex` 是连接级写锁**

理论铺垫在 6.8.3，具体原因见 6.13.4。

---

## 6.11 握手：`wsHandler` 的 11 步

源码：`internal/msggateway/ws_server.go:491-549`

这是**整个网关最关键的 60 行**。对照理论 6.3.5 的生命周期图看：

| # | 代码 | 行号 | 对应理论 |
|---|---|---|---|
| 1 | `newContext(w, r)` | `:493` | 生成 ConnID，准备上下文 |
| 2 | 检查连接数上限 | `:496-500` | 保护自己（6.8.4 背压） |
| 3 | `ParseEssentialArgs()` | `:503` | 从 URL 取 token/userID（6.4.3 时机 A） |
| 4 | `authClient.ParseToken()` | `:511` | gRPC 调 auth 服务验 token |
| 5 | `validateRespWithRequest()` | `:518` | 防"token 和 URL 参数不一致" |
| 6 | `websocket.Upgrade()` | `:524` | **协议升级（101）** |
| 7 | 可选首包响应 | `:529-534` | `isMsgResp=true` 时先回成功 |
| 8 | 计算 ping 周期 | `:538-541` | 只有 Web 平台（6.5.5） |
| 9 | `client := new(Client)` | `:543` | ⚠️ 没用对象池（6.18） |
| 10 | `ResetClient(...)` | `:544` | 初始化编码器、心跳、订阅表 |
| 11 | `registerChan <- client` + `go readMessage()` | `:547-548` | 注册 + 起读协程 |

### 逐步看关键代码

**第 2 步：连接数上限**

```go
if ws.onlineUserConnNum.Load() >= ws.wsMaxConnNum {
    ws.handlerError(connContext, w, r,
        servererrs.ErrConnOverMaxNumLimit.WrapMsg("over max conn num limit"))
    return
}
```

**这行代码的位置很讲究**：它在**鉴权之前**。也就是说，即使未鉴权的连接也会先被计数检查。

**为什么放在最前面？** 因为如果放在鉴权之后，攻击者可以用大量**无效 token** 的连接反复打进来——虽然每个都会被拒绝，但每次都要走一次 gRPC 鉴权（打 auth 服务）。**放在最前面，成本最低**。

⚠️ **但这里有个竞态**：`Load()` 和后续的 `Add(1)` 不是原子的。极端并发下可能超出上限几十个连接。实践中可接受（上限是软保护），但要知道这不是严格限流。

**第 3 步：解析参数**

```go
err := connContext.ParseEssentialArgs()
if err != nil {
    ws.handlerError(connContext, w, r, err)   // ← 此时还能返回 HTTP 错误
    return
}
```

注意 `handlerError` 的注释语义：**失败发生在 Upgrade 之前，所以是 HTTP 响应**。这正好印证了理论 6.4.4。

**第 4 步：同步调 auth 服务**

```go
resp, err := ws.authClient.ParseToken(connContext, connContext.GetToken())
```

🔴 **这是网关对 auth 服务的硬依赖**。

**"硬依赖"意味着什么？**

```
auth 服务挂了 → ParseToken 全部失败 → 所有新连接都建不起来
```

**已有的连接不受影响**（它们不需要重新鉴权），但**新用户完全无法登录**。这是单点故障。

**为什么不用本地验签代替？** 因为 JWT 验签需要密钥，如果网关本地有密钥，就能自己验，不必调 auth。但项目选择 RPC 调用，说明：
- 验签逻辑集中在 auth 服务（单一职责）；
- 可以在这里做额外校验（比如 token 是否被强制吊销——这需要查存储）。

**代价**：多一跳 RPC，且 auth 成为握手路径上的关键依赖。**这是一个明确的可用性 vs 集中化的取舍**。

**第 5 步：防止参数与 token 不一致**

```go
// ws_server.go:456-466
func (ws *WsServer) validateRespWithRequest(connContext *UserConnContext, resp *auth.ParseTokenResp) error {
    if resp.UserID != connContext.GetUserID() {
        return servererrs.ErrTokenNotValid ...
    }
    if resp.PlatformID != connContext.GetPlatformID() {
        return ...
    }
}
```

**这一步防的是什么？**

假设只验 token 不校验一致性：
- 你拿**自己的合法 token**（user001）；
- 但在 URL 里写 `sendID=user999`；
- 如果不校验，网关会认为"这是 user999 的连接"，于是你能收到 user999 的消息，还能以 user999 身份发消息——**完全的账号劫持**。

所以这一步是**必须的**，它不是重复劳动。

**第 6 步：Upgrade**

```go
conn, err := ws.websocket.Upgrade(w, r, nil)
```

`Upgrade` 内部做了三件事（对照理论 6.3.2）：
1. 校验请求头（`Upgrade`、`Connection`、`Sec-WebSocket-Key`、版本）；
2. 计算 `Sec-WebSocket-Accept`；
3. **劫持（Hijack）底层的 TCP 连接**，写出 101 响应，然后把这条 TCP 交给 WebSocket 读写器。

⚠️ **`Hijack` 这个词很重要**：它意味着从这一刻起，这个连接**脱离了 `net/http` 的管理**，`http.Server` 不再管它。所以后面所有的超时、关闭都要自己处理——这也是为什么需要手写读循环和心跳。

**第 9 步：对象池的"只放不取"**

```go
client := new(Client)                                  // :543 直接 new
```

但 `unregisterClient` 里：

```go
// ws_server.go:440 附近
ws.clientPool.Put(client)
```

**`clientPool` 定义了（`:155-159`）、放回了、但从来没人 `Get()`**。这是理论 6.8.5 提到的问题：**对象池被当垃圾桶**，不但没省下分配，还多了一次 Put 的开销，并且让 GC 无法及时回收（池中的对象持有 ctx、conn 引用）。

📌 **这是源码里的实现瑕疵**，面试时可以作为"我读代码时发现的改进点"提出。修复方式很简单：`client := ws.clientPool.Get().(*Client)`，并在 Put 前清空字段。

---

## 6.12 参数解析：四个坑的完整根因

源码：`internal/msggateway/context.go:93-168`

### 两种参数传递方式

```go
func (c *UserConnContext) ParseEssentialArgs() error {
    query := c.Req.URL.Query()
    if data := query.Get("v"); data != "" {
        return c.parseByJson(data)      // 新方式：v=<base64(json)>
    }
    return c.parseByQuery(query, c.Req.Header)   // 老方式：逐个 query 参数
}
```

**为什么要两种？**

| 方式 | 形态 | 优点 | 缺点 |
|---|---|---|---|
| A. 逐个 query | `?sendID=x&token=y&platformID=5...` | 可读、易调试 | 参数变多后 URL 很长；token 里有特殊字符要转义 |
| B. `v` 参数 | `?v=eyJzZW5kSUQiOi...` | 一个参数搞定，不怕转义 | 不可读，必须解码才知道内容 |

方式 B 是**新版 SDK 的做法**：把所有参数打包成 JSON，base64 后放进 `v`。这样即使以后加参数，URL 格式也不变（**向前兼容**）。

### 方式 A 的参数表（这就是"坑 1"的来源）

| 参数 | 常量 | 必填 | 缺了会怎样 |
|---|---|---|---|
| `sendID` | `WsUserID` | ✅ | `sendID is empty`（`:153-155`） |
| `token` | `Token` | ✅ | `token is empty`（`:150-152`） |
| `operationID` | `OperationID` | ✅ | `operationID is empty`（`:147-149`） |
| `platformID` | `PlatformID` | ✅ | **`platformID is not int`**（`:111-114`） |
| `isBackground` | `BackgroundStatus` | ✅ | **`ParseBool "" invalid syntax`**（`:126-129`） |
| `sdkType` | `SDKType` | ❌ | 默认 `"go"`（`:159-161`） |
| `compression` | `Compression` | ❌ | 不压缩 |
| `isMsgResp` | `SendResponse` | ❌ | false |
| `sdkVersion` | `SDKVersion` | ❌ | 空 |

**为什么 `isBackground` 缺了会报错，而 `sdkType` 缺了有默认值？**

看代码：

```go
// context.go:126-129
if isBackground := query.Get(BackgroundStatus); isBackground != "" {
    b, err := strconv.ParseBool(isBackground)
    ...
}
```

注意这里其实**有空值判断**（`!= ""`）。那为什么会报 `ParseBool "" invalid syntax`？

📌 **这正是"坑 1"的真实成因需要说清楚的地方**：
`ParseBool` 的报错说明代码**确实拿到了空字符串并去解析了**。可能的路径有两类：
- 走了 `parseByJson`（`v` 参数），JSON 里 `isBackground` 字段缺失，反序列化后是**零值**，再被当作字符串去 ParseBool；
- 或者某个 Header 取值路径返回空。

**结论（保守表述）**：无论具体走哪条路径，现象与结论一致——**`isBackground` 必须显式传值**，不能依赖"空则跳过"。这也是我在调试时踩到的。

⚠️ **这里我必须诚实**：具体是哪一行把空字符串喂给 `ParseBool` 的，需要你在出问题时打断点确认（📌 待验证）。**手册里不把推测写成结论**，这是我们的规矩。

### 方式 B：`v` 参数的解码

```go
// context.go:134-144
reqInfo, err := base64.RawURLEncoding.DecodeString(data)  // ① base64 解码
var info UserConnContextInfo
json.Unmarshal(reqInfo, &info)                            // ② JSON 解析
```

**注意 `base64.RawURLEncoding`**：
- `RawURLEncoding` 是 **URL 安全的 base64**，用 `-` 和 `_` 替代 `+` 和 `/`，并且**去掉 `=` 填充**；
- 为什么？因为参数要放进 URL，`+` 会被 URL 解码成空格，`=` 会造成解析歧义。

**这是一个很细但很典型的工程点**：任何要放进 URL 的 base64，都必须用 URL-safe 变体。面试时如果聊到"URL 传二进制"，这就是标准答案。

### "坑 4"：`sdkType` 的默认值

```go
// context.go:159-165
switch info.SDKType {
case "":
    info.SDKType = GoSDK        // ⚠️ 默认 go
case GoSDK, JsSDK:
default:
    return ErrConnArgsErr.WrapMsg("sdkType is invalid")
}
```

**为什么不默认 `js`？**

因为 OpenIM 的主力用户是**服务端集成方**（用 Go SDK 做机器人、做后台、做测试），默认 Go 能让他们"零配置可用"。浏览器只是其中一种客户端，必须显式声明。

**后果**：任何非 Go 客户端忘记传 `sdkType=js`，服务端就会用 Gob 解码它的 JSON 数据 → 直接失败。**这就是你踩的坑 4**。

✅ **实践建议**：写客户端时，**永远显式传 `sdkType`**，不要依赖默认值。

---

## 6.13 Client：一根连接的全部行为

源码：`internal/msggateway/client.go`

### 6.13.1 ResetClient：握手后的初始化

```go
// client.go:78-95（简化）
func (c *Client) ResetClient(ctx *UserConnContext, conn LongConn, server LongConnServer) {
    c.ctx = ctx
    c.conn = conn
    c.longConnServer = server
    c.UserID = ctx.GetUserID()
    c.PlatformID = ctx.GetPlatformID()
    c.token = ctx.GetToken()
    c.IsBackground = ctx.GetBackgroundStatus()
    c.IsCompress = ctx.GetCompression() == GzipCompressionProtocol
    c.SDKType = ctx.GetSDKType()

    if c.SDKType == GoSDK {
        c.Encoder = NewGobEncoder()      // ← 对应 6.12 坑 4
    } else {
        c.Encoder = NewJsonEncoder()
    }
    c.subUserIDs = make(map[string]struct{})
}
```

**这段代码把"握手阶段解析出的参数"固化成连接属性**。之后每条消息的处理都依赖这些属性（用哪个编码器、要不要解压、推不推）。

### 6.13.2 读循环：一连接一 goroutine

```go
// client.go:97-127
func (c *Client) readMessage() {
    defer func() {
        if r := recover(); r != nil {
            c.closedErr = ErrPanic
            log.ZPanic(c.ctx, "socket have panic err:", errs.ErrPanic(r))
        }
        c.close()                  // ← 无论如何退出都要关连接
    }()

    for {
        message, returnErr := c.conn.ReadMessage()   // ← 阻塞读
        if returnErr != nil {
            c.closedErr = returnErr
            return
        }
        if c.closed.Load() {                         // ← 已被别的路径关闭
            c.closedErr = ErrConnClosed
            return
        }
        if parseDataErr := c.handleMessage(message); parseDataErr != nil {
            c.closedErr = parseDataErr
            return
        }
    }
}
```

**四个细节，每一个都有原因**：

**① `defer recover()`**：任何一条消息处理中的 panic 都不能让整个进程崩溃。**一条坏消息只应该断一条连接**。

**② `defer c.close()`**：读循环退出 = 连接必须关闭。否则会泄漏 socket 和 goroutine。

**③ 阻塞读**：`ReadMessage()` 卡住时 goroutine 被运行时挂起，不占 CPU（理论 6.8.2）。

**④ `c.closed.Load()` 检查**：因为连接可能被**别的路径**关闭（被踢、心跳超时、网关主动断）。关的时候读循环可能正卡在 `ReadMessage()` 里，等它读到数据返回时才发现"我早该死了"。

**这解释了 10 万连接的成本**：

```
100,000 goroutine × 2KB 栈 ≈ 200MB
+ 100,000 个 socket fd（Linux 默认 ulimit -n 1024，必须调大！）
+ 读缓冲区（writeBufferSize 配置）
```

📌 **运维常识**：部署网关第一件事是 `ulimit -n 1000000`，否则几万连接就 `too many open files`。这不是代码问题，是系统配置。

### 6.13.3 分发：`handleMessage` 的完整流程

```go
// client.go:130-197（简化但保留关键分支）
func (c *Client) handleMessage(message []byte) error {
    // ① 解压（对应 6.12 的 compression 参数）
    if c.IsCompress {
        message, err = c.longConnServer.DecompressWithPool(message)
    }

    // ② 从对象池取 Req，用完归还
    var binaryReq = getReq()
    defer freeReq(binaryReq)

    // ③ 解码（Gob 或 JSON）
    if err := c.Encoder.Decode(message, binaryReq); err != nil {
        return err
    }

    // ④ 参数校验
    if err := c.longConnServer.Validate(binaryReq); err != nil {
        return err
    }

    // ⑤ 🔴 防伪造：请求里的 sendID 必须等于连接的真实 userID
    if binaryReq.SendID != c.UserID {
        return errs.New("exception conn userID not same to req userID", ...)
    }

    // ⑥ 构建链路上下文（operationID 用于全链路追踪）
    ctx := mcontext.WithMustInfoCtx([]string{
        binaryReq.OperationID,
        binaryReq.SendID,
        constant.PlatformIDToName(c.PlatformID),
        c.ctx.GetConnID(),
    })

    // ⑦ 按指令码分发
    switch binaryReq.ReqIdentifier {
    case WSGetNewestSeq:        resp, messageErr = c.longConnServer.GetSeq(...)
    case WSSendMsg:             resp, messageErr = c.longConnServer.SendMessage(...)
    case WSSendSignalMsg:       resp, messageErr = c.longConnServer.SendSignalMessage(...)
    case WSPullMsgBySeqList:    resp, messageErr = c.longConnServer.PullMessageBySeqList(...)
    case WSPullMsg:             resp, messageErr = c.longConnServer.GetSeqMessage(...)
    case WSGetConvMaxReadSeq:   resp, messageErr = c.longConnServer.GetConversationsHasReadAndMaxSeq(...)
    case WsPullConvLastMessage: resp, messageErr = c.longConnServer.GetLastMessage(...)
    case WsLogoutMsg:           resp, messageErr = c.longConnServer.UserLogout(...)
    case WsSetBackgroundStatus: resp, messageErr = c.setAppBackgroundStatus(...)
    case WsSubUserOnlineStatus: resp, messageErr = c.longConnServer.SubUserOnlineStatus(...)
    default:
        return fmt.Errorf("ReqIdentifier failed, sendID:%s ...", ...)
    }

    // ⑧ 回复
    return c.replyMessage(ctx, binaryReq, messageErr, resp)
}
```

**第 ⑤ 步是本篇最重要的安全检查**：

```go
if binaryReq.SendID != c.UserID {
    return errs.New("exception conn userID not same to req userID", ...)
}
```

**没有这行会怎样？**

```
攻击者用自己的合法账号登录 → 拿到合法 token → 建立连接
然后构造 Req{sendID: "受害者ID", data: 恶意消息}
→ 服务端如果信任 Req 里的 sendID，就会以受害者身份发消息
```

这就是**水平越权（IDOR）**。握手阶段校验的是"token 属于谁"，这一步校验的是"消息声称的发送者是不是连接的所有者"，**两者缺一不可**。

📌 **面试高频**：问"IM 怎么防止伪造发送者"，答案就是这两道关卡。

**第 ⑦ 步的分发是"表驱动"的**：`ReqIdentifier` 是枚举，`switch` 到对应的方法。这种写法：
- 优点：清晰、编译器能检查重复 case；
- 缺点：新增指令要改这里（**开闭原则的反面**），且 `switch` 变长后性能略降（实际可忽略）。

如果要做成可扩展的，可以用 `map[int32]HandlerFunc` 注册表。**这是一个可以提的改进点**。

### 6.13.4 写消息：为什么必须加锁

```go
// client.go:286-298
func (c *Client) writeBinaryMsg(resp Resp) error {
    if c.closed.Load() {
        return nil
    }
    encodedBuf, _ := c.Encoder.Encode(resp)

    c.w.Lock()                    // ← 连接级写锁
    defer c.w.Unlock()

    // 压缩后 conn.WriteMessage(...)
}
```

**为什么写要加锁，读不需要？**

因为**同一条连接可能被多个 goroutine 同时写**：

| 写来源 | 在哪个 goroutine | 场景 |
|---|---|---|
| 响应 | 读协程 | 你发消息，回你 `SendMsgResp` |
| 推送 | 网关事件循环 / gRPC 处理协程 | 别人发消息给你 |
| 心跳 ping | 心跳 goroutine | Web 平台定时 ping |
| 踢人通知 | 事件循环 | 你被别处登录踢掉 |

**WebSocket 协议规定：同一时刻只能有一个写操作**（不能用多帧交织，否则帧边界会错乱，接收方解析崩溃）。

**不加锁会怎样？** 两个 goroutine 同时 `WriteMessage`，底层 `net.Conn.Write` 是原子的（单次系统调用不会交错），但**一帧可能需要多次 Write**（大消息分片），交错后就变成垃圾数据 → 对端协议错误 → 断连。

**注意这里有个顺序问题**：
```go
encodedBuf, _ := c.Encoder.Encode(resp)   // 锁外编码
c.w.Lock()                                 // 锁内只做写
```

**编码在锁外，写操作在锁内**——把耗时操作（序列化）放在临界区外，缩短持锁时间。**这是个好习惯**，虽然 Encoder 本身可能有自己的状态（Gob 编码器是有状态的流式编码器，这里其实有并发隐患 📌 待验证）。

⚠️ **`defer c.w.Unlock()` 前面如果 panic**，`defer` 仍然会执行，不会死锁。但如果 `WriteMessage` 永久阻塞（对端不读、TCP 窗口满），锁会一直被持有 → 其他写全堵住。所以 `writeWait = 10s` 这个写超时是必须的。

### 6.13.5 推送：构造一帧推送消息

```go
// client.go:246-266
func (c *Client) PushMessage(ctx context.Context, msgData *sdkws.MsgData) error {
    var msg sdkws.PushMessages
    conversationID := msgprocessor.GetConversationIDByMsg(msgData)

    m := map[string]*sdkws.PullMsgs{
        conversationID: {Msgs: []*sdkws.MsgData{msgData}},
    }
    if msgprocessor.IsNotification(conversationID) {
        msg.NotificationMsgs = m       // 通知消息走独立字段
    } else {
        msg.Msgs = m                   // 普通消息
    }

    data, _ := proto.Marshal(&msg)
    resp := Resp{
        ReqIdentifier: WSPushMsg,      // ← 2001
        OperationID:   ...,
        Data:          data,
    }
    return c.writeBinaryMsg(resp)
}
```

**关键设计：推送和响应共用 `Resp` 结构，靠 `ReqIdentifier` 区分。**

| `ReqIdentifier` | 方向 | 客户端的处理 |
|---|---|---|
| 1003 | 客户端 → 服务端 | — |
| **2001** | **服务端 → 客户端** | **这是别人发来的消息** |
| 2002 | 服务端 → 客户端 | 你被踢了 |
| 3001 | 服务端 → 客户端 | 数据错误 |

**为什么复用同一个结构？** 因为客户端只需要一个解析入口：解出 `Resp`，看 `reqIdentifier` 就知道该干嘛。如果推送用另一个结构，客户端要维护两套解码逻辑。

**这就是"统一信封"的价值**（第 07 篇会讲完整的协议设计）。

**为什么 `NotificationMsgs` 和 `Msgs` 要分开？**
- 普通消息：单聊/群聊消息，会影响未读数、会话排序；
- 通知消息：入群通知、踢人通知、群资料变更，**不产生未读、不排序会话**。

分开字段让客户端一眼区分，不必解析内容再判断。

---

## 6.14 事件循环与连接表

### 6.14.1 `userMap`：二级 map

源码：`internal/msggateway/user_map.go`

```go
type userMap struct {
    lock sync.RWMutex
    data map[string]*UserPlatform    // userID → 该用户所有平台的连接
    ch   chan UserState              // 10000 缓冲，状态变更通知
}
```

```go
type UserPlatform struct {
    PlatformIDs map[int32]*PlatformConn   // platformID → 连接集合
}
```

**结构是三层的**：

```
userID
  └── platformID (1=iOS, 2=Android, 5=Web, ...)
        └── [conn1, conn2, ...]     ← 为什么是数组？
```

**为什么最底层是数组而不是单个连接？**

因为存在**同一平台多连接**的合法场景：
- 多标签页打开同一个 Web IM；
- 同型号两台手机用同一账号（如果策略允许）；
- 重连过渡期，旧连接还没清掉（6.15 的同 token 问题）。

**如果设计成单连接**，新连接会覆盖旧连接，旧连接变"孤儿"——既收不到消息，又占着 socket，而且无法被踢（表里已经没有它了）。**用数组是这个问题的正解**。

### 6.14.2 关键方法

| 方法 | 作用 | 行号 |
|---|---|---|
| `GetAll(userID)` | 拿该用户所有连接（推送用） | `:93` |
| `Get(userID, platformID)` | 拿某平台连接，返回三元组 `(clients, userExists, clientExists)` | `:103` |
| `Set(userID, client)` | 加连接 | `:119` |
| `DeleteClients(userID, clients)` | 删连接，返回"该用户是否已无连接" | `:134` |
| `UserState()` | 状态变更 channel | `:183` |

**`Get` 返回三元组的设计很讲究**：

```go
func (m *userMap) Get(userID string, platformID int32) ([]*Client, bool, bool)
//                                                  连接   用户存在  该平台有连接
```

**为什么要区分"用户存在"和"平台有连接"？**

因为踢人逻辑需要判断：
- 用户完全不在线（`userExists=false`）→ 不用踢，直接注册；
- 用户在线但这平台没连接（`clientExists=false`）→ 不用踢；
- 用户在线且这平台有连接（`clientExists=true`）→ 按策略判断踢不踢。

**如果只返回一个 bool**，调用方分不清这两种情况，就会写出"用户在其他平台在线时也误踢"的 bug。

### 6.14.3 `DeleteClients` 的隐患

```go
// user_map.go:134-150（简化）
func (m *userMap) DeleteClients(userID string, clients []*Client) bool {
    m.lock.Lock()
    defer m.lock.Unlock()

    deleteClient := func(c *Client) {
        // ⚠️ 用 RemoteAddr 作为删除键
        if old, ok := platformConn.clients[c.ctx.GetRemoteAddr()]; ok {
            delete(...)
        }
    }
    ...
}
```

⚠️ **用 `RemoteAddr` 当 map key 是个隐患**：

**为什么不用指针？** 因为踢人时拿到的 `*Client` 可能是**从别的节点序列化过来的副本**（跨节点踢人），指针地址不同但逻辑上是同一个连接。

**为什么不用 ConnID？** 这是更好的选择（ConnID 是 md5(remoteAddr+时间戳)，全局唯一且稳定）。

**用 RemoteAddr 的问题**：
- 同一 NAT 后面的两台设备 → 出口 IP 相同 → `RemoteAddr` 可能相同 → **误删**；
- 但端口不同，所以严格说 `RemoteAddr` 是 IP:Port，实际上很难撞。

📌 **保守结论**：实践中撞车概率低（端口随机），但**设计上不如 ConnID 严谨**。这是可以提的改进点。

---

## 6.15 多端互踢：实现细节

### 6.15.1 触发点

踢人逻辑由三处触发：
1. 本节点新连接注册时（`registerClient`）；
2. 其他节点通过 gRPC 通知（`MultiTerminalLoginCheck`）；
3. 事件循环消费 `kickHandlerChan`。

### 6.15.2 核心函数

```go
// ws_server.go:333-437（结构示意）
func (ws *WsServer) multiTerminalLoginChecker(ctx context.Context, info *kickHandler) {
    newClient := info.newClient

    // ① 同 token 过滤（🔴 关键，见理论 6.7.3）
    checkSameTokenFunc := func(oldClients []*Client) []*Client {
        var needKick []*Client
        for _, c := range oldClients {
            if c.token == newClient.token {
                continue                  // 同 token 不踢
            }
            needKick = append(needKick, c)
        }
        return needKick
    }

    // ② 按策略算出要踢谁
    needKick := ws.checkKickStrategy(newClient, oldClients)
    needKick = checkSameTokenFunc(needKick)

    // ③ 从连接表删除 + 通知客户端 + 失效旧 token
    ws.clients.DeleteClients(newClient.UserID, needKick)
    for _, c := range needKick {
        c.KickOnlineMessage()
    }
    ws.authClient.InvalidateToken(ctx, &pbAuth.InvalidateTokenReq{
        PreservedToken: newClient.token,   // ← 保留新 token，失效其他的
        UserID:         newClient.UserID,
        PlatformID:     newClient.PlatformID,
    })
}
```

**第 ③ 步的 `PreservedToken` 是精髓**：

```go
InvalidateToken(..., PreservedToken: newClient.token)
```

**语义**：把该用户该平台的其他 token 全部作废，**只保留新连接用的这个**。

**为什么必须失效旧 token？** 因为如果只断连接不作废 token，被踢的客户端可以**立刻用同一个 token 重连**，形成"踢了又连、连了又踢"的死循环。**踢人必须连凭据一起废掉**。

### 6.15.3 跨节点广播

```go
// ws_server.go:221-257
func (ws *WsServer) sendUserOnlineInfoToOtherNode(ctx context.Context, client *Client) error {
    conns, _ := ws.disCov.GetConns(ctx, ws.msgGatewayConfig.Discovery.RpcService.MessageGateway)
    if len(conns) == 0 || (len(conns) == 1 && ws.disCov.IsSelfNode(conns[0])) {
        return nil                                  // 单节点，不用广播
    }

    wg := errgroup.Group{}
    wg.SetLimit(concurrentRequest)                  // 3
    for _, v := range conns {
        if ws.disCov.IsSelfNode(v) {
            continue
        }
        wg.Go(func() error {
            msgClient := msggateway.NewMsgGatewayClient(v)
            _, err := msgClient.MultiTerminalLoginCheck(ctx, &msggateway.MultiTerminalLoginCheckReq{...})
            return err                                // ⚠️ 注意这里
        })
    }
    return wg.Wait()
}
```

**两个设计点**：

**① `SetLimit(3)` 是背压**（理论 6.8.4）

如果有 20 个网关节点，注册一个用户就并发打 20 个 RPC，会把所有节点同时打一遍。限制 3 个并发，把瞬间压力摊平。

**② 错误处理：`return err` 后 `wg.Wait()`**

这里有个细节值得分析：`wg.Wait()` 会返回**第一个非 nil 错误**，但**不会中断其他 goroutine**（errgroup 默认不 cancel，除非用了 `WithContext`）。

调用方对错误怎么处理？如果调用方**忽略错误只记日志**，那么语义是"广播尽力而为，失败不影响本节点注册成功"——这是**最终一致性**的合理选择，因为：
- 如果因为节点 B 网络抖动就让用户连不上，体验很差；
- 漏掉的踢人请求，下次该用户再登录时会再次触发检查。

📌 **但要确认调用方是否忽略**（📌 待验证）。如果不忽略而直接返回错误，那握手就会因为"某个网关节点挂了"而失败——那是设计缺陷。

---

## 6.16 推送入口：push 服务怎么找到连接

源码：`internal/msggateway/hub_server.go`

### 6.16.1 单用户推送

```go
// hub_server.go:121-155（简化）
func (s *Server) pushToUser(ctx context.Context, userID string, msgData *sdkws.MsgData) *SingleMsgToUserResults {
    clients, ok := s.LongConnServer.GetUserAllCons(userID)
    if !ok {
        return &SingleMsgToUserResults{UserID: userID}    // 不在线
    }

    for _, client := range clients {
        // iOS 后台特殊处理
        if client.IsBackground && client.PlatformID == constant.IOSPlatformID {
            result.ResultCode = int64(servererrs.ErrIOSBackgroundPushErr.Code())
            continue                                       // 跳过，让上层走离线推送
        }
        if err := client.PushMessage(ctx, msgData); err != nil {
            // 记录失败
        } else if _, ok := s.pushTerminal[client.PlatformID]; ok {
            result.OnlinePush = true
        }
    }
    return result
}
```

**iOS 后台为什么要跳过？**（理论 6.5.1 的延伸）

iOS 系统在 App 进入后台后会**冻结进程**，WebSocket 连接虽然还在，但 App 不处理数据。此时推消息：
- 数据写进了 socket 缓冲区，App 醒来前不会被读；
- 系统可能在几分钟后直接杀掉连接。

所以**推了也没意义**，不如直接返回错误码 `ErrIOSBackgroundPushErr`，让上层改走 **APNs 离线推送**（系统级通知栏）。

📌 **这是"客户端能力差异影响服务端逻辑"的典型例子**。做 IM 必须理解各平台的连接生命周期。

### 6.16.2 群发推送：为什么用内存队列

```go
// hub_server.go:157-206（简化）
ch := make(chan *SingleMsgToUserResults, len(req.PushToUserIDs))
var count atomic.Int64
count.Add(int64(len(req.PushToUserIDs)))

for i := range req.PushToUserIDs {
    userID := req.PushToUserIDs[i]
    err := s.queue.PushCtx(ctx, func() {          // ← 有界队列
        ch <- s.pushToUser(ctx, userID, req.MsgData)
        if count.Add(-1) == 0 {
            close(ch)
        }
    })
}
```

队列初始化：

```go
// hub_server.go:81
queue: memamq.NewMemoryQueue(512, 1024*16)
```

参数含义：**最大 512 个并发任务，单任务 16KB**。

**如果没有这个队列，直接 `go pushToUser(...)` 会怎样？**

群有 5000 人（大群、全员在线）：
```
5000 个 goroutine 同时启动
每个都做：查连接表 + 编码 + 压缩 + 写 socket
→ 5000 个 goroutine 同时抢 CPU
→ 5000 次 socket write（IO 密集，内核缓冲区可能满）
→ 该节点的其他用户消息延迟飙升
```

**这就是"惊群"效应**：一个群消息把整个网关节点的处理能力打满。

**有界队列的作用**：把 5000 个任务压到 512 并发执行，其余排队。**总吞吐不变，但峰值被削平**（理论 6.8.4）。

**代价**：大群推送会变慢（5000 人 ÷ 512 ≈ 10 批）。但这是**可接受的**——消息晚 100ms 送达没人感知，节点被打挂所有人都不能用。

⚠️ **注意 `ch` 的容量是 `len(PushToUserIDs)`**，这是为了**避免写 channel 阻塞**（否则 worker 写 ch 时可能阻塞，导致队列卡住）。容量刚好等于任务数，保证每个任务都能写入。

---

## 6.17 心跳的代码实现

### 常量定义

```go
// constant.go:46-58
writeWait      = 10 * time.Second
pongWait       = 30 * time.Second
pingPeriod     = (pongWait * 9) / 10    // 27s
maxMessageSize = 51200                  // 50KB
```

### 谁在发 ping

```go
// ws_server.go:538-541
var pingInterval time.Duration
if connContext.GetPlatformID() == constant.WebPlatformID {
    pingInterval = pingPeriod
}

client.ResetClient(connContext, NewWebSocketClientConn(conn, maxMessageSize, pongWait, pingInterval), ws)
```

**只有 Web 平台传了非零的 `pingInterval`**，其他平台传 0（不主动 ping）。

### 读超时怎么实现的

```go
// client_conn.go 里（NewWebSocketClientConn 的封装）
conn.SetReadDeadline(time.Now().Add(pongWait))
conn.SetPongHandler(func(string) error {
    conn.SetReadDeadline(time.Now().Add(pongWait))   // 收到 pong 就续命
    return nil
})
```

**这是 Go 里做超时的标准套路**：

```
设置一个"死亡时间线"（ReadDeadline）
    ↓
每次收到 pong，就把死亡时间线往后推 30 秒
    ↓
如果 30 秒内没有任何数据（连 pong 都没有）
    ↓
ReadMessage 返回 timeout 错误 → 读循环退出 → 连接关闭
```

**为什么用 Deadline 而不是定时器？** 因为 `SetReadDeadline` 是**内核级**的（底层 socket 超时），不需要额外 goroutine 轮询，成本极低。如果用 `time.AfterFunc` 每 30 秒检查一次，10 万连接就是 10 万个定时器。

📌 **这是"用系统能力代替应用层轮询"的典型优化**，值得在面试里提。

### 业务层心跳（移动端）

```go
// message_handler.go:19-22
TextPing  // 客户端发的业务心跳
TextPong  // 服务端的应答
```

**两套心跳并存的原因**：
- **协议层 ping/pong**：只检测"连接是否还通"，不进入业务逻辑；
- **业务层 TextPing/TextPong**：SDK 自己控制节奏，可以携带业务信息（如当前前台/后台状态）。

移动端用业务层心跳，是因为 SDK 更清楚系统的休眠调度，能选择合适时机。**服务端只需要能回应答**。

---

## 6.18 并发模型与资源控制总结

### 每根连接的 goroutine 数

| goroutine | 何时起 | 何时退 | 数量 |
|---|---|---|---|
| `readMessage` | 握手成功（`ws_server.go:548`） | 读失败/连接关闭 | 1 / 连接 |
| 心跳 ping（Web） | `ResetClient` 内部控制 | 连接关闭 | 0~1 / 连接 |
| 写 | **不单独起**，同步加锁写 | — | 0 |

**所以每连接 1~2 个 goroutine**。

### 全局 goroutine

| goroutine | 数量 | 作用 |
|---|---|---|
| 事件循环 | 1 | 注册/注销/踢人 |
| http.Server | 1 | 监听端口 |
| 订阅广播 | 1 | 在线状态变更分发 |
| Redis 在线状态同步 | 1 | 定时上报 |

### 资源控制手段一览

| 手段 | 位置 | 对应理论 | 目的 |
|---|---|---|---|
| `sync.Pool` 复用 `Req` | `message_handler.go:48-67` | 6.8.5 | 减少 GC |
| `sync.Pool` 复用 gzip writer | `compressor.go` | 6.8.5 | 压缩开销 |
| `atomic.Bool` 关闭标志 | `client.go` | 6.8.6 | 无锁判状态 |
| `atomic.Int64` 计数 | `ws_server.go:60-61` | 6.8.6 | 无锁统计 |
| channel 缓冲 1000 | `ws_server.go:160-162` | 6.8.4 | 削峰 |
| `memamq` 512 并发 | `hub_server.go:81` | 6.8.4 | 推送背压 |
| `errgroup.SetLimit(3)` | `ws_server.go` | 6.8.7 | 跨节点广播限流 |
| 连接数上限 | `ws_server.go:496` | 6.8.4 | 保护自己 |
| `ReadDeadline` | `client_conn.go` | — | 心跳超时 |

---

## 6.19 设计评价：好在哪里，问题在哪

真正读懂一个项目，不是复述它做了什么，而是能判断**哪里设计得好、哪里有隐患**。

### 设计得好的三处

**① 用 channel 串行化代替锁**

注册/注销/踢人涉及多步状态变更，用锁很难保证整段一致。串行化用"单 goroutine"免费获得原子性，代码也更好读。**这是 Go 里处理复杂共享状态的推荐做法**。

**② 同 token 过滤**

承认分布式状态收敛有延迟，在业务规则上做兼容。这是**有分布式经验的人才写得出的细节**。

**③ 把鉴权放在协议升级之前**

未授权连接根本建立不起来，从源头节省资源。**安全设计的第一原则是"不要给坏人分配资源"**。

### 有明显瑕疵的三处

**① `clientPool` 只放不取**（`ws_server.go:155` vs `:440` vs `:543`）

定义了池、Put 了、但从来 Get。不但没优化，还让对象在池里多活一轮 GC，并且池中对象持有 ctx/conn 引用，可能延长内存生命周期。

**改法**：
```go
client := ws.clientPool.Get().(*Client)
// ... 使用
// Put 前清零字段，避免泄漏引用
```

**② `DeleteClients` 用 `RemoteAddr` 作删除键**（`user_map.go:145`）

应该用 `ConnID`（全局唯一、稳定）。`RemoteAddr` 在 NAT 环境有理论上的碰撞风险。

**③ `handleMessage` 的巨型 switch**

新增指令码必须改这个函数，违反开闭原则。更好的做法：
```go
var handlers = map[int32]func(ctx, *Req) ([]byte, error){...}
```

⚠️ **但要公允**：这三处都不是致命 bug，属于"可以做得更好"。面试时说"我发现了 XX 问题，如果我来改会 XX"，比说"这个项目很完美"更有说服力。

---

# 第三部分 实验：亲手验证

## 实验 1：用 curl 看握手（最简单）

WebSocket 握手本质是一个 HTTP 请求，可以用 curl 手动发：

```bash
curl -i -N \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  "http://127.0.0.1:10001/msg_gateway?sendID=user001&token=xxx&platformID=5&operationID=t1&isBackground=false"
```

**预期**：
- token 无效 → `HTTP/1.1 4xx` + JSON 错误体（证明鉴权在 Upgrade 之前）
- token 有效 → `HTTP/1.1 101 Switching Protocols`（然后 curl 会卡住，因为协议已切换）

📌 **这个实验能直观验证理论 6.4.3 和 6.4.4**：失败时你拿到的是 HTTP 响应，不是 WS 帧。

⚠️ 端口需按你的配置确认（网关默认 10001，以 `config/` 为准）。

## 实验 2：发一个文本帧，看报错

用浏览器控制台：

```js
const ws = new WebSocket("ws://127.0.0.1:10001/msg_gateway?sendID=user001&token=xxx&platformID=5&operationID=t1&isBackground=false&sdkType=js");
ws.onopen = () => {
    ws.send("hello");        // ← 文本帧
};
ws.onclose = (e) => console.log("closed", e.code, e.reason);
```

**预期**：连接被关闭，服务端日志出现 `not support text message type`。

**这验证了理论 6.3.4**：读循环只接受 binary 帧。

## 实验 3：观察心跳

```js
const ws = new WebSocket("ws://...sdkType=js");
let t0 = Date.now();
ws.onmessage = (e) => {
    if (typeof e.data === "string") console.log("text:", e.data);
};
// 浏览器开发者工具 → Network → WS → Messages
// 观察 27 秒左右是否有 ping（浏览器自动回 pong，界面上可能只显示收到 ping）
```

**验证**：`pingPeriod = 27s` 确实生效（理论 6.5.4）。

## 实验 4：验证"同 token 不踢"

1. 用同一个 token 建两条连接（两个浏览器标签页）；
2. 观察是否都被踢。

**预期**：**都不被踢**（同 token 过滤）。

然后：
3. 重新登录拿一个**新 token**，用新 token 建连接；
4. 观察旧 token 的连接是否被踢。

**预期**：旧连接被踢（`WSKickOnlineMsg`），且旧 token 失效（再用旧 token 握手会失败）。

📌 这个实验同时验证了 6.15 的两条逻辑：同 token 不踢、旧 token 失效。

⚠️ **注意**：如果配置的策略是 `DefalutNotKick`，则任何情况都不踢，实验会失败。先确认 `config/openim-msggateway.yml` 里的 `multiLogin.policy`。

---

# 第四部分 自测题

## 理论层（不看书回答）

1. HTTP 的三个特征是什么？哪一个让"服务器主动推送"变成不可能？
2. 短轮询的延迟为什么是"间隔/2"？为什么不能靠调小间隔解决？
3. 长轮询比短轮询好在哪？代价是什么？
4. SSE 为什么不适合 IM？举两个具体理由。
5. WebSocket 是"新协议"还是"HTTP 的升级"？升级发生在哪一步？
6. `101` 和 `200` 的语义差别是什么？为什么中间代理会在这里出问题？
7. `Sec-WebSocket-Accept` 是怎么算的？算它的**目的**是什么（不是加密）？
8. 客户端帧为什么必须掩码？
9. opcode 0x1 / 0x2 / 0x9 / 0xA 分别是什么？
10. 为什么鉴权要放在 Upgrade 之前？放在之后有什么风险？
11. JWT 的 payload 是加密的吗？为什么不能放密码？
12. 什么叫"半开连接"？举三个产生场景。
13. TCP keepalive 为什么不够用？
14. `pingPeriod` 为什么必须小于 `pongWait`？取等了会怎样？
15. 网关为什么必须独立于业务服务？如果合并会怎样？
16. 多节点部署时，给 user001 推消息有哪三种找连接的方案？各自的代价？
17. "同 token 不踢"解决的是什么问题？不做会怎样？
18. 为什么"一连接一 goroutine"在 Go 里可行，在 Java 里不行？
19. channel 串行化相比 mutex，优势和代价各是什么？
20. 什么是背压？没有背压的系统在高峰时会怎样？

## 代码层（要能指出文件）

21. 握手的 11 步在哪几行？哪一步调了 auth 服务？
22. `onlineUserNum` 和 `onlineUserConnNum` 有什么区别？为什么要分开？
23. `binaryReq.SendID != c.UserID` 这行防的是什么攻击？
24. 写消息为什么加锁？锁的范围为什么只包住 `WriteMessage` 而不包编码？
25. `userMap` 最底层为什么是数组不是单连接？
26. `Get` 为什么返回三元组？
27. `InvalidateToken` 的 `PreservedToken` 是什么语义？不传会怎样？
28. 群发推送为什么用 `memamq`？参数 512 是什么含义？
29. iOS 后台为什么直接返回错误码？
30. `clientPool` 的问题是什么？怎么改？

---

# 第五部分 面试话术

## 30 秒版（被问"讲讲你的 IM 项目网关"）

> "网关是独立于业务服务的一层，专门持有 WebSocket 长连接。因为它有状态——内存里存着几十万条连接——不能像无状态 RPC 那样随便重启。
>
> 握手阶段同步调 auth 服务验 token，验完才 Upgrade，这样未授权连接根本建立不起来。每条连接一个读 goroutine 阻塞在 ReadMessage，写操作共享一把连接级锁，因为响应、推送、心跳、踢人四个来源都会写同一条连接，而 WebSocket 不允许并发写帧。"

## 3 分钟版（被追问"技术难点"）

> "我认为有三个点值得说。
>
> **第一是并发模型。** 每连接一个读 goroutine，10 万连接就是 10 万 goroutine，靠 Go 的 2KB 小栈撑住，总共约 200MB；换成线程模型 1MB 栈直接爆。写操作用一把连接级锁串行化，因为 WebSocket 协议不允许并发写帧。
>
> **第二是连接管理用 channel 串行化。** 注册、注销、踢人全进一个事件循环，一个 goroutine 顺序消费。因为注册涉及查旧连接、判断策略、踢人、跨节点广播、更新计数多步，用锁很难保证整段原子性，还容易在锁里做网络 IO 导致死锁。串行化用架构消除了竞态。
>
> **第三是多端互踢的同 token 过滤。** 多网关部署时，客户端从 A 断开重连到 B，A 的旧连接还没清理，会出现同 token 两条连接。直接按策略踢的话，用户会把自己踢下线，表现为反复掉线。项目里同 token 不踢，本质是承认分布式状态收敛有延迟，在业务规则上做兼容。同时踢人时会调 InvalidateToken 作废旧 token，只保留新的，否则被踢的客户端拿旧 token 立刻重连，形成死循环。"

## 追问预判

| 可能被问 | 怎么答 |
|---|---|
| "10 万连接内存多少？" | goroutine 约 200MB + 每个 socket 的读写缓冲区 + Client 结构体。要调 `ulimit -n`。 |
| "网关挂了怎么办？" | 客户端自动重连（SDK 有重连逻辑），重连后按本地 maxSeq 拉增量消息——这就是 seq 机制的价值。 |
| "为什么不把所有服务合并？" | 有状态和无状态的生命周期不同。合并后业务服务重启会断开所有连接。 |
| "心跳为什么用 ReadDeadline 不用定时器？" | Deadline 是内核 socket 超时，零额外 goroutine；定时器 10 万连接就是 10 万个对象。 |
| "这个网关有什么问题？" | 三处：clientPool 只放不取、DeleteClients 用 RemoteAddr、巨型 switch 违反开闭原则。 |
| "怎么支撑更多连接？" | 水平扩网关节点 + 路由表优化（避免广播）；单机受限于 fd 数和内存，可上多路复用（如 epoll 分片）。 |

---

**下一篇**：[07 消息协议与编解码](07-消息协议与编解码.md) —— 讲清楚 WS 上跑的字节到底长什么样，为什么是双层编码。
