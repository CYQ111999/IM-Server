# 22 Redis 使用与缓存策略

> **本篇定位**：Part5 第二篇。21 篇讲「真相存在哪」（MongoDB），本篇讲「真相前面的那道墙」（Redis）。
> **前置**：12（seq 分配器，用了 Redis Lua）、14（用户水位）、15（在线状态 ZSET）、16（token HASH）。这四篇都用到了 Redis，但都是「用到哪讲到哪」。本篇把它们串成一个完整的缓存体系。
> **阅读方式**：第一部分纯理论（Redis 原理 + 缓存的三大经典问题 + 一致性方案）；第二部分是本仓库 30+ 个 key 的完整清单和 rockscache 的实现。
> **本篇标记**：🔴 重点　🟡 难点　⚠️ 易错　🧪 实验　📌 待验证

---

# 第一部分　理论：缓存不是一个「加个 Redis」的事

## 1.1 Redis 为什么快

先破除一个误解：**Redis 是单线程的**。

这句话在 6.0 之后就不完全对了（网络 IO 多线程了），但**命令执行仍然是单线程**。所以问题变成：单线程为什么还能跑到 10 万 QPS？

四个原因，按重要性排序：

**第一，数据在内存里。** 内存随机读 ~100ns，SSD 随机读 ~100μs，机械盘 ~10ms。差 1000 倍到 10 万倍。这是最根本的原因——**Redis 快不是因为代码写得好，是因为它不碰磁盘**。

**第二，单线程没有锁和上下文切换。** 多线程处理共享数据要加锁，加锁就有竞争、有等待、有上下文切换（一次切换 ~1-10μs）。单线程天然没有这些问题。这就是所谓的「**单线程反而更快**」的反直觉现象。

**第三，IO 多路复用。** 一个线程用 `epoll` 同时监听几万个 socket，哪个有数据就处理哪个。不需要「一连接一线程」。

**第四，数据结构本身高效。** 比如 `Hash` 在小数据量时用**压缩列表（ziplist/listpack）**——一块连续内存，省掉了指针开销；数据量大了才转成哈希表。`ZSet` 用**跳表（skiplist）**——范围查询 O(log n)，比平衡树实现简单。

> 🔴 **面试点**：为什么用跳表而不用红黑树？
> 三个理由：① 范围查询跳表只要顺着底层链表走，红黑树要中序遍历；② 跳表实现简单（几十行 vs 几百行），不容易出 bug；③ 并发场景下跳表只需要局部锁，红黑树旋转要锁整棵树（虽然 Redis 单线程用不上，但设计上更友好）。

**单线程的代价**：一个慢命令会阻塞所有人。所以 **`keys *`、`flushall`、大 key 的 `hgetall` 都是生产事故**。Redis 用「慢查询日志」和「大 key 扫描」来防这个。

## 1.2 五种数据结构，以及它们各自解决什么问题

不要背「String/List/Hash/Set/ZSet」，要记「**什么场景用什么**」：

### String：单值

```
SET key value EX 60
GET key
INCR counter
```

**适用**：计数器、分布式锁（`SET key val NX EX 10`）、简单的序列化对象。

**注意**：String 存 JSON 对象也能用，但**改一个字段要读出来改完再写回去**（读-改-写），并发下会丢更新。所以对象用 Hash 更好。

### Hash：对象

```
HSET user:1 name "张三" age 20
HGET user:1 name
HGETALL user:1
```

**适用**：一个对象有多个字段，且**只需要读/改其中一部分**。

**关键优势**：`HSET` 是**字段级**的原子操作。100 个人同时 `HSET user:1 age`，不会互相覆盖。

> 🔴 **OpenIM 里 Hash 是绝对主力**。rockscache 的缓存格式、token 状态、seq 分配器、用户水位，全是 Hash。原因就是「字段级原子性」——缓存要存 `value` + `lockUntil` + `lockOwner` 三个字段，如果存成 JSON String，就得整体读改写，锁就失效了。

### List：队列

```
LPUSH queue msg
RPOP queue
```

**适用**：简单队列、时间线。

**OpenIM 没用 List**。因为队列需求被 Kafka/Redis Stream 接管了（List 不支持消费组、不支持 ack）。

### Set：去重集合

```
SADD friends:1 2 3 4
SISMEMBER friends:1 2
SINTER friends:1 friends:2      // 交集
```

**适用**：去重、集合运算（共同好友、标签）。

> ⚠️ **有意思的发现**：OpenIM **几乎不用 Set**。好友列表 (`FRIEND_IDS:`) 和群成员 (`GROUP_MEMBER_IDS:`) 这类「天然适合 Set」的数据，实际存的是**序列化后的 JSON 字符串**（存在 rockscache 的 Hash `value` 字段里）。
>
> 为什么？因为要配合 `TagAsDeleted` 的失效机制——rockscache 只认 `value` 字段，Set 类型塞不进去。这是一个「**为了统一缓存框架，牺牲了数据结构优势**」的取舍。
>
> 代价：`SISMEMBER friends:1 2` 是 O(1)；而「查 JSON 数组里有没有 2」是 O(n)，且**整个数组都要从 Redis 传到应用层**。好友数上千时，这个差异很明显。

### ZSet：带分数的有序集合

```
ZADD online:1 1735689600 ios 1735689600 android
ZRANGEBYSCORE online:1 (1735689600 +inf)    // 未过期的
```

**适用**：排行榜、延时队列、**在线状态**（15 篇）。

**为什么在线状态用 ZSet 而不是 `SET key 1 EX 60`？** 15 篇详细论证过，核心是：**ZSet 的 score 可以当作「到期时间戳」，一个 key 里能放多个平台，每个平台独立过期，支持按分数范围查询和批量清理**。而 String 的 EX 是整个 key 的 TTL，无法按平台粒度控制。

## 1.3 key 的设计规范

一个 key 的设计要回答四个问题：

**① 命名空间怎么隔离？**

OpenIM 用**前缀 + 冒号**：`CONVERSATION:`, `FRIEND_IDS:`, `ONLINE:`。这是 Redis 社区的事实标准（Redis 作者 antirez 提的）。

⚠️ **但有个坑**：`CONVERSATION:` 和 `CONVERSATION_IDS:` 是**两个不同前缀**，前者是后者的字符串前缀！

```
CONVERSATION:user1:si_a_b       ← 单个会话
CONVERSATION_IDS:user1          ← 会话 ID 列表
```

如果哪天要用 `SCAN CONVERSATION:*` 批量清理，会把 `CONVERSATION_IDS:*` 也扫出来。**前缀命名冲突是 Redis 里最常见的隐形 bug**。这就是为什么 `localcache/init.go` 里做失效广播时，用的是**精确前缀匹配**而不是模糊匹配（24 篇会讲）。

**② 需要 TTL 吗？**

**所有缓存都必须有 TTL**。没有 TTL 的 key 是「内存泄漏的定时炸弹」——程序 bug 导致某个 key 没被删，它就永久占着内存。

OpenIM 的 TTL 策略：

| 类型 | TTL | 位置 |
|---|---|---|
| 业务缓存（好友/群/会话/用户） | 12 小时 | `friendExpireTime`、`groupExpireTime` |
| rockscache 的锁 | 11 秒 | `rocksCacheTimeout` |
| token 状态 | accessExpire（配置，默认 24h 级别） | `token.go:50` |
| 在线状态 | 30 分钟 | `cachekey.OnlineExpire` |
| 已读水位 | 30 天 📌 | `seq_user.go` |
| 消息缓存 | 📌 待验证 | `msg.go` |

> ⚠️ **12 小时这个值偏长**。缓存的 TTL 越长，命中率越高，但「脏数据存活时间」也越长。OpenIM 的策略是**不靠 TTL 保证一致性，靠主动失效**（下面 1.5 讲），TTL 只是兜底。这个思路是对的：**TTL 是安全网，不是一致性方案**。

**③ 会不会成为大 key？**

大 key 的定义：String > 10KB，Hash/Set/ZSet 元素 > 5000。

大 key 的危害：
- **阻塞**：单线程执行 `HGETALL` 一个大 Hash，其他人全等着
- **网络**：一次传输几十 MB
- **删除**：`DEL` 一个大 key 也是阻塞的（Redis 4.0 后用 `UNLINK` 异步删）

OpenIM 里最容易成为大 key 的是 `GROUP_MEMBER_IDS:{groupID}`——一个 10 万人的群，序列化后的 JSON 数组超过 1MB。

📌 **待验证**：有没有对大群做特殊处理。我在 `cache/redis/group.go` 里看到 `GetGroupMemberIDs` 是整体读，没有分页。如果真有大群，这里会是个隐患。

**④ 怎么批量操作？**

三种方式，能力递增：

| 方式 | 原子性 | 能否条件判断 | 往返次数 |
|---|---|---|---|
| **Pipeline** | ❌（可能部分成功） | ❌ | 1 |
| **Lua** | ✅ | ✅ | 1 |
| **MULTI/EXEC** | ✅ | ❌（不能读中间结果） | 2 |

**Pipeline** 只是把多个命令打包发过去，服务端按顺序执行，**中间任何一条失败，后面的照常执行**。

**Lua** 在 Redis 里是原子的（执行期间不处理其他命令），而且能读中间结果做条件判断。**这是它比 MULTI 强的地方**——MULTI 里你没法「先读再决定写不写」。

> 🔴 **为什么 OpenIM 的 seq 分配必须用 Lua？** 因为要做「读 LOCK → 判断是否过期 → 写回」这三步，且必须原子。Pipeline 做不到原子，MULTI 做不到条件判断。只有 Lua 可以。12 篇详细讲过这两个脚本。

## 1.4 三大缓存问题（面试必问，但很少有人讲清「为什么」）

### 缓存穿透：查一个不存在的数据

```
请求 user:999999（不存在）
→ 缓存没有
→ 查数据库，也没有
→ 不写缓存（因为没数据）
→ 下次同样的请求，又来一遍
```

如果攻击者用随机 ID 刷，每次都要打到数据库，缓存等于没有。

**两种解法**：

**方案 A：缓存空值。** 查不到也写一个「空」标记，TTL 设短一点（比如 5 秒）。

OpenIM 的 `localcache` 就是这么做的：

```go
// pkg/localcache/option.go:16-17
localSuccessTTL: time.Minute,        // 命中/成功的缓存 1 分钟
localFailedTTL:  time.Second * 5,    // 失败（含记录不存在）只缓存 5 秒
```

配置文件里也对应：

```yaml
# config/local-cache.yml
successExpire: 300    # 成功的缓存 5 分钟
failedExpire: 5       # 失败只缓存 5 秒（防穿透）
```

**为什么要区分两个 TTL？** 因为「不存在」这个结论**可能是临时的**——用户可能下一秒就被创建了。缓存 5 秒，既能挡住密集的重复查询，又不会让「新建的用户查不到」持续太久。这个细节很多人不知道。

**方案 B：布隆过滤器。** 把所有存在的 ID 放进布隆过滤器，查之前先过一遍。不存在的一定被拦住，存在的可能误判（假阳性）。

⚠️ **OpenIM 没用布隆过滤器**。因为 IM 的用户 ID 是动态增长的，布隆过滤器**不支持删除**（删了会误判更多），维护成本高。用空值缓存已经够了。

### 缓存击穿：热点 key 突然过期

```
某个 10 万人的群，GROUP_INFO:g1 的缓存到点了
同一瞬间 1000 个请求打进来
1000 个都发现缓存没了
1000 个都去查数据库
数据库瞬间被打穿
```

**关键区别**：穿透是「查不存在的数据」，击穿是「热点 key 过期时的高并发」。

**标准解法是「互斥锁 + 双重检查」**：只让一个请求去回源，其他等着。

OpenIM 用的 rockscache **就是干这个的**，而且做得比标准方案更细（1.5 节展开）。

### 缓存雪崩：大量 key 同时过期

```
凌晨 0 点，一批缓存统一设置了 12 小时 TTL
中午 12 点，这批缓存同时过期
数据库瞬间被压垮
```

**标准解法：给 TTL 加随机抖动。**

```go
// rockscache 源码（client.go:131）
ex := expire - c.Options.Delay - time.Duration(rand.Float64()*c.Options.RandomExpireAdjustment*float64(expire))
```

OpenIM 的配置：

```go
// pkg/common/storage/cache/redis/batch.go:21
opts.RandomExpireAdjustment = 0.2
```

意思是：实际 TTL = 名义 TTL - 延迟删除时间 - **随机 (0~20%) × TTL**。

12 小时的缓存，实际落在 9.6~12 小时之间，**打散在一个 2.4 小时的窗口里**。不会同时过期。

> 🔴 **注意这里有个精妙之处**：随机抖动是**减**不是加（`expire - rand`），而且还要再减去 `Delay`。为什么？因为 rockscache 的 `TagAsDeleted` 会把 key 的 TTL 改成 `Delay`（延迟删除），如果存的时候不带这个余量，会出现「刚要过期又被标记删除」的竞态细节问题。这是实现细节，理解到「抖动打散雪崩」这一层就够面试用了。

## 1.5 缓存一致性：五种方案的取舍

这是缓存最难的部分。先明确目标和约束：

```
数据库是真相，缓存是副本
写数据库和写缓存是两个操作，不可能同时成功
所以「缓存和数据库永远一致」是不可能的
```

能做的是：**让不一致的窗口尽量短、尽量小**。

### 方案对比表

| 方案 | 做法 | 不一致窗口 | 问题 |
|---|---|---|---|
| ① 先更数据库，再更缓存 | 写库 → 写缓存 | 微秒级 | **并发写时缓存会被旧值覆盖** |
| ② 先删缓存，再更数据库 | 删缓存 → 写库 | 写库耗时 | 删完到写库之间，读请求回填旧值 |
| ③ 先更数据库，再删缓存 | 写库 → 删缓存 | 极小 | **业界默认方案（Cache Aside）** |
| ④ 延迟双删 | 删缓存 → 写库 → 睡一会 → 再删 | 睡眠时间 | 睡眠多久说不清 |
| ⑤ 锁 + 版本号 | 读时加锁，写时校验 | 0 | 性能差 |

**为什么 ③ 是默认方案？**

考虑最坏情况：写请求 A（写库 → 删缓存）和读请求 B（读缓存未命中 → 查库 → 回填）并发。

```
t1: B 读缓存，未命中
t2: B 查数据库，拿到旧值
t3: A 写数据库（新值）
t4: A 删缓存（但缓存里本来就没东西，白删）
t5: B 回填缓存（**旧值**）← 不一致产生
```

这个问题的窗口是「B 从查库到回填的这段时间」，通常是毫秒级。而且它要求 B 的查库**恰好发生在 A 写库之前**，概率不高。

**方案 ② 的问题更严重**：删缓存后到写库前，如果有读请求进来，会回填旧值，然后 A 写完库不再删缓存 → **不一致会一直持续到 TTL 过期**（12 小时）。

所以：**「先写库后删缓存」比「先删缓存后写库」安全**。这是面试要答出的核心逻辑。

> ⚠️ **`先更数据库，再更缓存`（方案①）为什么不行？** 因为两个写请求并发时：
> ```
> A: 写库 value=1
> B: 写库 value=2
> B: 写缓存 value=2
> A: 写缓存 value=1   ← 缓存变成 1，库是 2，永久不一致
> ```
> 缓存的写入顺序和数据库的写入顺序可能相反。**这是「更新缓存」比「删除缓存」危险的根本原因**——删除是幂等的，更新不是。

### rockscache 的方案：锁 + 版本 + 延迟删

rockscache 是一个专门解决缓存一致性的库（`github.com/dtm-labs/rockscache`），思路是 **「单飞 + 分布式锁 + 延迟过期」**。

它的核心设计：**一个缓存 key 在 Redis 里不是 String，而是一个 Hash，三个字段**：

```
KEY = DELETE_CACHE_USER 里的某个业务 key，例如 USER_INFO:user1
VALUE = {
  "value":      "{...json...}",   ← 真正的缓存内容
  "lockUntil":  "1735689600",     ← 锁到期时间戳（Unix 秒）
  "lockOwner":  "a1b2c3d4"        ← 持有锁的请求 ID（uuid）
}
```

**为什么必须用 Hash？** 因为要**原子地**做「读 value 的同时判断锁状态」。如果 value 和锁分成两个 key，就有竞态。用 Hash + Lua，一次原子操作搞定。

这个设计直接影响了两件事：
1. Redis 里**所有 rockscache 管理的 key 都是 HASH 类型**（不是 String）
2. 你不能用 `GET` 去看缓存内容，得用 `HGET key value`

> 🧪 **实验 22-1**：进 Redis 验证这一点
> ```bash
> docker exec -it redis redis-cli -a <密码> -n 0
> > TYPE USER_INFO:imAdmin
> hash
> > HGETALL USER_INFO:imAdmin
> 1) "value"
> 2) "{\"userID\":\"imAdmin\",...}"
> ```

### 读路径：strongFetch

```go
// rockscache client.go:216（简化）
func (c *Client) strongFetch(ctx, key, expire, fn) (string, error) {
    owner := shortuuid.New()
    r, err := c.luaGet(ctx, key, owner)
    for err == nil && r[1] != nil && r[1] != locked {   // 被别人锁着
        time.Sleep(c.Options.LockSleep)                // 等着
        r, err = c.luaGet(ctx, key, owner)
    }
    if r[1] != locked {   // 缓存有值且没被锁
        return r[0].(string), nil
    }
    return c.fetchNew(ctx, key, expire, owner, fn)     // 抢到锁了，回源
}
```

关键在 `luaGet` 这个脚本（`client.go:143`）：

```lua
local v = redis.call('HGET', KEYS[1], 'value')
local lu = redis.call('HGET', KEYS[1], 'lockUntil')
if lu ~= false and tonumber(lu) < tonumber(ARGV[1]) or lu == false and v == false then
    -- 锁已过期，或者根本没值 → 抢锁
    redis.call('HSET', KEYS[1], 'lockUntil', ARGV[2])    -- ARGV[2] = now + LockExpire
    redis.call('HSET', KEYS[1], 'lockOwner', ARGV[3])    -- ARGV[3] = owner
    return { v, 'LOCKED' }
end
return {v, lu}
```

**逐句解释**：

| 判断 | 含义 |
|---|---|
| `lu ~= false and tonumber(lu) < now` | 有锁但已过期 → 可以抢 |
| `lu == false and v == false` | 没锁也没值（首次访问）→ 可以抢 |
| 其他 | 有值且无锁，或锁还在有效期内 → 返回现状 |

返回 `{v, 'LOCKED'}` 表示「你抢到锁了，去回源吧」。返回 `{v, lu}` 表示「缓存可用」或「别人锁着，`lu` 是它的到期时间」。

> 🔴 **这里有个设计要点**：抢锁不是靠 `SETNX`，而是靠 **「检查 + 写」在同一个 Lua 里完成**。如果用 `SETNX`，就要先 `GET` 判断有没有值，再 `SETNX` 判断能不能抢锁，两次操作之间有竞态。
>
> 而且注意 `lockUntil` 是**时间戳**不是 TTL。为什么？因为需要判断「锁是否过期」来决定要不要抢。用 TTL 的话，`TTL` 返回 -2（不存在）/-1（永久）和数字三种，逻辑更绕。用时间戳，一个 `tonumber` 比较就完事。

### 写路径：luaSet 必须校验 lockOwner

```lua
-- rockscache client.go:161
local o = redis.call('HGET', KEYS[1], 'lockOwner')
if o ~= ARGV[2] then
        return          -- ← 锁不是我的，直接放弃写
end
redis.call('HSET', KEYS[1], 'value', ARGV[1])
redis.call('HDEL', KEYS[1], 'lockUntil')
redis.call('HDEL', KEYS[1], 'lockOwner')
redis.call('EXPIRE', KEYS[1], ARGV[3])
```

**这一行 `if o ~= ARGV[2] then return end` 是整个方案的安全阀**。

场景：请求 A 抢到锁，去数据库查了 30 秒（假设查得很慢），锁 11 秒就过期了。请求 B 抢到锁，查完写入了新值。这时 A 才查完回来写。

**没有这行校验** → A 用旧值覆盖 B 的新值 → 脏数据。
**有了这行** → A 发现 `lockOwner` 已经被改成 B 了，放弃写入。

> 🔴 **这个模式叫「带 owner 校验的分布式锁」**，是所有分布式锁的标准写法（不是 rockscache 独创）。核心：**解锁/写回时必须验证「锁还是不是我的」**，否则会误删别人的锁。
>
> 12 篇的 seq 分配器里有一模一样的模式（`HGET LOCK ~= lockValue` 就放弃）。这不是巧合——**同一个作者写的代码，模式会复用**。

### 写库之后：TagAsDeleted 的「延迟删除」

缓存失效不是 `DEL`，而是这样（`client.go:91`）：

```lua
redis.call('HSET', KEYS[1], 'lockUntil', 0)    -- 锁立刻失效
redis.call('HDEL', KEYS[1], 'lockOwner')
redis.call('EXPIRE', KEYS[1], ARGV[1])         -- ARGV[1] = Delay（秒）
```

**注意：value 没有被删！** 只是：
1. 把 `lockUntil` 设成 0 → 下次 `luaGet` 会认为「锁已过期，可以抢」
2. 把 TTL 改成 `Delay` → 这个 key 在 `Delay` 秒后自然过期

**为什么不直接 `DEL`？**

这是「延迟双删」的一种更优雅的实现。回到 1.5 的时序图：

```
t1: 读请求 B 读缓存，未命中
t2: B 查数据库，拿到**旧值**
t3: 写请求 A 写数据库（新值）
t4: A 删缓存（DEL）
t5: B 回填缓存（旧值）← 脏
```

用了 `TagAsDeleted` 之后：

```
t4: A 标记删除（lockUntil=0，TTL=Delay）
t5: B 回填 → 走 luaSet，发现 lockOwner 对不上（或锁已被清）→ 放弃
    或者 B 走的是 weakFetch 路径，抢到锁写入 → 但 A 的写库已经完成，B 拿到的是新值
```

**关键在于**：`lockUntil=0` 让这个 key 处于「**可被抢占**」状态，而不是「不存在」。这样任何后续的读请求都会走「抢锁 → 重新回源」的路径，而不是「读到旧值直接用」。

> 🔴 **这是我在这套代码里看到的最精妙的设计**。传统「删除缓存」的问题是「删除后的第一个请求会回填旧值」。改成「标记为可抢锁」，就等于告诉所有后续请求「这个值可能脏了，谁抢到锁谁去刷新」。
>
> 代价是**每次失效后都有一个请求要等回源**（`strongFetch` 的 `Sleep` 循环），这是用延迟换一致性。

### OpenIM 的配置选择

```go
// pkg/common/storage/cache/redis/batch.go:16-24
func GetRocksCacheOptions() *rockscache.Options {
    opts := rockscache.NewDefaultOptions()
    opts.LockExpire = rocksCacheTimeout           // 11 秒
    opts.WaitReplicasTimeout = rocksCacheTimeout  // 11 秒
    opts.StrongConsistency = true                 // ← 强一致模式
    opts.RandomExpireAdjustment = 0.2             // 20% 抖动
    return &opts
}
```

**`StrongConsistency = true` 意味着走 `strongFetch`**：缓存正在被刷新时，读请求**等待**（`Sleep` 循环），而不是先返回旧值再异步刷新。

对比 `weakFetch`：读到旧值立刻返回，同时起一个 goroutine 去刷新。这叫「**逻辑过期**」方案，性能好但有一段时间读到旧值。

> 🟡 **OpenIM 选强一致的理由**：IM 的读操作大多是「发消息前的校验」（是不是好友、在不在群、有没有被禁言）。这些校验如果读到旧值，会导致**该拦的没拦住**（比如刚被踢出群的人又发了一条消息）。所以宁愿慢一点，也要准。
>
> **什么时候该选 weakFetch？** 读多写少、对一致性不敏感的场景（比如「这个用户的昵称是什么」——昵称改错 5 分钟没人在意）。OpenIM 一刀切选了 strong，📌 待验证是否有按场景区分的可能。

---

# 第二部分　源码：30+ 个 key 与三层缓存

## 2.1 key 全景表

先给结论：**OpenIM 的 Redis key 只有三种物理类型**。

| 物理类型 | 谁在用 | 占比 |
|---|---|---|
| **HASH** | rockscache 业务缓存（好友/群/会话/用户）、token 状态、seq 分配器、seq_user 水位 | 绝大多数 |
| **ZSET** | 在线状态（`ONLINE:`） | 1 类 |
| **STRING** | 临时 key（`token.go:215` 的 5 分钟占位） | 极少 |

**没有用 Set、没有用 List、没有用 Stream（redismq 用的是 Stream 但走的是框架层）**。

完整清单（按业务域分组）：

### 用户与认证

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `USER_INFO:{userID}` | HASH | 12h | 用户信息缓存 |
| `USER_GLOBAL_RECV_MSG_OPT_KEY:{userID}` | HASH | 12h | 全局免打扰设置 |
| `UID_PID_TOKEN_STATUS:{userID}:{platform}` | HASH | accessExpire | **token 白名单**（field=token，value=0/1） |
| `UID_PID_TOKEN_STATUS::TEMPORARY:{userID}:{platform}:{token}` | STRING/HASH | 短 | 临时 token（管理员代登录） |

### 好友

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `FRIEND_IDS:{ownerUserID}` | HASH | 12h | 好友 ID 列表（JSON 数组） |
| `FRIEND_INFO:{owner}:{friend}` | HASH | 12h | 好友详情（备注/置顶） |
| `COMMON_FRIENDS_IDS:{userID}` | HASH | 12h | 双向好友（互相都是好友的） |
| `BLACK_IDS:{ownerUserID}` | HASH | 12h | 黑名单 ID 列表 |
| `IS_FRIEND:{possible}:{user}` | 本地缓存 | 5min | 是否好友（**有依赖关系**） |
| `IS_BLACK:{user}:{possible}` | 本地缓存 | 5min | 是否拉黑（**有依赖关系**） |
| `FRIEND_MAX_VERSION:{owner}` | HASH | 12h | 好友版本号（增量同步） |

### 群组

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `GROUP_INFO:{groupID}` | HASH | 12h | 群信息 |
| `GROUP_MEMBER_IDS:{groupID}` | HASH | 12h | 群成员 ID 列表（**易成大 key**） |
| `GROUP_MEMBER_INFO:{groupID}:{userID}` | HASH | 12h | 群成员详情 |
| `GROUP_MEMBERS_HASH2:{groupID}` | HASH | 12h | 成员列表的哈希（快速判断"有没有变"） |
| `JOIN_GROUPS_KEY:{userID}` | HASH | 12h | 我加入的群 |
| `GROUP_MEMBER_NUM_CACHE:{groupID}` | HASH | 12h | 群成员数 |
| `GROUP_ROLE_LEVEL_MEMBER_IDS:{groupID}:{level}` | HASH | 12h | 按角色分组的成员（查管理员列表） |
| `GROUP_MEMBER_MAX_VERSION:{groupID}` | HASH | 12h | 成员版本号 |
| `GROUP_JOIN_MAX_VERSION:{userID}` | HASH | 12h | 入群版本号 |

### 会话

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `CONVERSATION:{owner}:{convID}` | HASH | 12h | 单个会话 |
| `CONVERSATION_IDS:{owner}` | HASH | 12h | 我的会话 ID 列表 |
| `CONVERSATION_IDS_HASH:{owner}` | HASH | 12h | 会话列表哈希 |
| `NOT_NOTIFY_CONVERSATION_IDS:{owner}` | HASH | 12h | 免打扰的会话 |
| `PINNED_CONVERSATION_IDS:{owner}` | HASH | 12h | 置顶的会话 |
| `RECV_MSG_OPT:{owner}:{convID}` | HASH | 12h | 会话的接收选项 |
| `CONVERSATION_HAS_READ_SEQ:{owner}:{convID}` | HASH | 12h | 已读水位（会话级） |
| `CONVERSATION_NOT_RECEIVE_MESSAGE_USER_IDS:{convID}` | HASH | 12h | 这个会话不接收消息的人（大群免打扰） |
| `SUPER_GROUP_RECV_MSG_NOT_NOTIFY_USER_IDS:{groupID}` | HASH | 12h | 超级群免打扰名单 |
| `CONVERSATION_USER_MAX:{userID}` | HASH | 12h | 用户会话最大版本 |

### seq 与水位

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `MALLOC_SEQ:{convID}` | HASH | 无/长期 | **seq 号段**（`MaxSeq`/`MinSeq`/`LastSeq`/`Lock`/`LockValue`） |
| `MALLOC_MIN_SEQ:{convID}` | HASH | 无/长期 | 会话清理水位 |
| `SEQ_USER_MAX:{convID}:{userID}` | HASH | 无/长期 | 用户拉取上界 |
| `SEQ_USER_MIN:{convID}:{userID}` | HASH | 无/长期 | 用户拉取下界 |
| `SEQ_USER_READ:{convID}:{userID}` | HASH | 30 天 📌 | 用户已读水位 |

> ⚠️ **seq 类 key「无 TTL」是刻意的**。因为这些是**唯一可信的水位**，一旦过期，就要回源 Mongo。而 Mongo 里的 `seq` 集合只有在号段耗尽时才更新——平时 Redis 里的值比 Mongo 新。让 Redis 过期 = **seq 回退 = 消息重复或丢失**。
>
> 这是「缓存」和「状态」的区别：**缓存可以丢，状态不能丢**。seq 在 Redis 里是**状态**，不是缓存。

### 在线与推送

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `ONLINE:{userID}` | ZSET | 30min（score 到期） | 在线状态（member=平台，score=到期时间戳） |
| `USER_BADGE_UNREAD_COUNT_SUM:{userID}` | STRING | 短 | 角标未读总数 |
| `FCM_TOKEN:{account}:{platform}` | STRING | - | 推送 token 📌（命名遗留，17 篇提过） |

### 消息与对象存储

| Key | 类型 | TTL | 用途 |
|---|---|---|---|
| `MSG_CACHE:{convID}:{seq}` | HASH | 📌 | 单条消息缓存 |
| `SEND_MSG_FAILED_FLAG:{id}` | STRING | 📌 | 发送失败标记 |
| `OBJECT:{engine}:{name}` | STRING | - | 对象是否存在（秒传） |
| `S3:{engine}:{name}` | STRING | - | S3 key 映射 |
| `MINIO:IMAGE:{key}` | STRING | - | 图片元信息（宽高） |
| `MINIO:THUMBNAIL:{format}:w{w}:h{h}:{key}` | STRING | - | 缩略图 |

### Pub/Sub 频道

| 频道 | 用途 |
|---|---|
| `DELETE_CACHE_AUTH` | auth 缓存失效广播 |
| `DELETE_CACHE_USER` | user 缓存失效广播 |
| `DELETE_CACHE_GROUP` | group 缓存失效广播 |
| `DELETE_CACHE_FRIEND` | friend 缓存失效广播 |
| `DELETE_CACHE_CONVERSATION` | conversation 缓存失效广播 |
| `online_change` | 在线状态变化广播（15 篇） |

> 🔴 **这张表能直接回答「你们的 Redis 用来做什么」这个面试题**。答案不是「做缓存」，而是四件事：
> 1. **缓存**（业务数据，Hash + rockscache）
> 2. **状态**（token 白名单、seq 水位、在线状态）——这些不是缓存，丢了会出事
> 3. **分布式锁**（seq 的 Lock/LockValue、rockscache 的 lockUntil/lockOwner）
> 4. **消息广播**（Pub/Sub 做缓存失效和在线状态通知）

## 2.2 自研 Lua：四个函数，零业务调用

`pkg/common/storage/cache/redis/lua_script.go` 有 127 行，定义了四个批量操作：

```go
// lua_script.go:15-47
setBatchWithCommonExpireScript = redis.NewScript(`
local expire = tonumber(ARGV[1])
for i, key in ipairs(KEYS) do
    redis.call('SET', key, ARGV[i + 1])
    redis.call('EXPIRE', key, expire)
end
return #KEYS
`)

setBatchWithIndividualExpireScript = redis.NewScript(`
local n = #KEYS
for i = 1, n do
    redis.call('SET', KEYS[i], ARGV[i])
    redis.call('EXPIRE', KEYS[i], ARGV[i + n])
end
return n
`)

deleteBatchScript = redis.NewScript(`
for i, key in ipairs(KEYS) do
    redis.call('DEL', key)
end
return #KEYS
`)

getBatchScript = redis.NewScript(`
local values = {}
for i, key in ipairs(KEYS) do
    local value = redis.call('GET', key)
    table.insert(values, value)
end
return values
`)
```

**先看一个细节**：`callLua` 里有 `NOSCRIPT` 处理：

```go
// lua_script.go:52-59
r := script.EvalSha(ctx, rdb, keys, args)
if redis.HasErrorPrefix(r.Err(), "NOSCRIPT") {
    if err := script.Load(ctx, rdb).Err(); err != nil {
        r = script.Eval(ctx, rdb, keys, args)      // 加载失败 → 直接 EVAL 全量脚本
    } else {
        r = script.EvalSha(ctx, rdb, keys, args)   // 加载成功 → 用 SHA 再试
    }
}
```

这是标准做法：**先 `EVALSHA`（只传 40 字节的 SHA1），服务端没有这个脚本时再 `EVAL`（传整个脚本）或 `SCRIPT LOAD`**。省了每次都传几 KB 脚本。

> ⚠️ **但这里有个判断写反了**：`Load` 成功之后才用 `EvalSha`，`Load` 失败反而用 `Eval`——逻辑是对的（Load 成功后脚本已在服务端，EvalSha 可用；Load 失败只能用全量 Eval）。不过 `Load` 失败通常是连接问题，`Eval` 大概率也会失败。这段代码的正确性没问题，只是「Load 失败还继续尝试」意义不大。

**关键事实**：这四个函数**只被 `lua_script_test.go` 调用**，业务代码零引用。

```bash
$ grep -rn "LuaSetBatch\|LuaDeleteBatch\|LuaGetBatch" --include="*.go" .
./pkg/common/storage/cache/redis/lua_script.go:67:func LuaSetBatchWithCommonExpire(...)
./pkg/common/storage/cache/redis/lua_script.go:88:func LuaSetBatchWithIndividualExpire(...)
./pkg/common/storage/cache/redis/lua_script.go:111:func LuaDeleteBatch(...)
./pkg/common/storage/cache/redis/lua_script.go:116:func LuaGetBatch(...)
./pkg/common/storage/cache/redis/lua_script_test.go:11,21,26,44,49,57   ← 只有测试
```

**为什么没人用？** 因为出现了更好的替代方案：rockscache 的 `FetchBatch2` 和 `TagAsDeletedBatch2` 直接处理批量（**还带集群槽位分组**），比手写批量 Lua 更完整。

> 📌 **这 127 行是死代码**。它的设计意图是「用一次 Lua 调用代替 N 次 SET」，解决网络往返问题。但实际业务都走了 rockscache，所以从未启用。
>
> 面试价值：这是一个「**自研了轮子，然后被更好的轮子替代，但没删掉**」的典型案例。如果你要在简历里写「我优化了 XX」，先确认它真的在被调用。

## 2.3 ProcessKeysBySlot：集群环境下的批量操作

这个函数是**集群分片的核心**（`redis_shard_manager.go:150`）：

```go
func ProcessKeysBySlot(ctx, redisClient, keys, processFunc, opts...) error {
    // 1. 按哈希槽分组
    slots, err := groupKeysBySlot(ctx, redisClient, keys)
    // 2. 并发处理每组（限流 3）
    g, ctx := errgroup.WithContext(ctx)
    g.SetLimit(config.concurrentLimit)
    for slot, singleSlotKeys := range slots {
        batches := splitIntoBatches(singleSlotKeys, config.batchSize)   // 默认 50 个一批
        for _, batch := range batches {
            g.Go(func() error { return processFunc(ctx, slot, batch) })
        }
    }
    return g.Wait()
}
```

**为什么需要按槽分组？**

Redis Cluster 把 key 空间分成 **16384 个哈希槽**，每个槽属于一个节点。**一个命令涉及的所有 key 必须在同一个槽**，否则报 `CROSSSLOT` 错误。

```
MSET k1 v1 k2 v2
→ 如果 k1 和 k2 在不同槽 → 报错
```

所以批量操作必须先算出每个 key 的槽，然后把同槽的 key 归成一批，发给对应节点。

`groupKeysBySlot` 的实现（`redis_shard_manager.go:110`）：

```go
func groupKeysBySlot(ctx, redisClient, keys) (map[int64][]string, error) {
    clusterClient, isCluster := redisClient.(*redis.ClusterClient)
    if isCluster && len(keys) > 1 {
        pipe := clusterClient.Pipeline()
        cmds := make([]*redis.IntCmd, len(keys))
        for i, key := range keys {
            cmds[i] = pipe.ClusterKeySlot(ctx, key)   // CLUSTER KEYSLOT 命令
        }
        _, err := pipe.Exec(ctx)                      // pipeline 一次算完所有槽
        ...
    }
    // 非集群模式：全部放槽 0
    slots[0] = keys
}
```

**两个细节**：

1. **用 pipeline 批量算槽**。算槽也是一次网络往返，100 个 key 逐个算就是 100 次往返。用 pipeline 合成 1 次。
2. **非集群模式直接放槽 0**。这是「用类型断言做能力探测」的又一个例子（21 篇的 `setName` 探测、这里的 `*redis.ClusterClient` 断言）。**没有配置开关，看实际客户端类型**。

**并发限流 3 是有讲究的**：

```go
const (
    defaultBatchSize       = 50
    defaultConcurrentLimit = 3
)
```

为什么是 3？因为 `errgroup.SetLimit(3)` 意味着最多 3 个 goroutine 同时跑。槽的数量可能上百个，如果全并发，会瞬间打出上百个并发请求到 Redis，把连接池打满（`poolSize: 100`）。

> 🟡 **调优思路**：如果 Redis 是集群（多节点），可以提高到 8-16（每个节点 2-3 个并发）。如果是单机，3 已经够了（单线程处理，并发多了只是排队）。**这个值应该跟部署形态相关，但代码里硬编码了**——这是一个可以改进的点。

## 2.4 token 的 HASH 结构

16 篇讲过逻辑，这里看存储细节（`cache/redis/token.go`）：

```go
// token.go:33 登录时写入
if err := c.rdb.HSet(ctx, key, token, flag).Err(); err != nil {
    return err
}

// token.go:47-50 刷新时（带 TTL 重置）
if err := c.rdb.HSet(ctx, key, token, flag).Err(); err != nil { return err }
if err := c.rdb.Expire(ctx, key, c.accessExpire).Err(); err != nil { return err }

// token.go:62 校验时全读
m, err := c.rdb.HGetAll(ctx, cachekey.GetTokenKey(userID, platformID)).Result()
```

**key 与 field 的分工**：

```
KEY   = UID_PID_TOKEN_STATUS:user1:ios     ← 用户 + 平台
FIELD = eyJhbGciOiJIUzI1NiIs...            ← 完整的 JWT 字符串
VALUE = "0" 或 "1"                          ← 0 正常 / 1 被踢
```

**为什么用「token 字符串」做 field？**

因为要支持**同端多 token**（一个用户在 iOS 上可能有多个 token：换设备、重装 App 但旧 token 还没过期）。用 token 做 field，`HGET key token` 就是 O(1) 的精确查询。

**为什么 value 是 "0"/"1" 而不是直接删除 field？**

这是为了让「被踢」和「不存在」可区分（16 篇讲过）：
- `HGET` 返回 `"1"` → 用户被踢了（`ErrTokenKicked`）
- `HGET` 返回 `nil`（field 不存在）→ token 无效（`ErrTokenNotExist`）

这两个错误对客户端的意义完全不同：前者提示「你的账号在其他设备登录了」，后者提示「请重新登录」。**用状态值而不是删除来表达语义**，是一个值得学的技巧。

## 2.5 online 的 ZSET（15 篇的存储层）

```go
// cache/redis/online.go:38
members, err := s.rdb.ZRangeByScore(ctx, s.getUserOnlineKey(userID), &redis.ZRangeBy{
    Min: strconv.FormatInt(time.Now().Unix(), 10),   // 只取 score >= now 的
    Max: "+inf",
}).Result()
```

**ZSET 结构**：

```
KEY    = ONLINE:user1
MEMBER = "1" (iOS) / "2" (Android) / "5" (Web)    ← 平台 ID
SCORE  = 1735689600                                ← 到期时间戳
```

**为什么用「到期时间戳」做 score？**

因为这样能：
1. `ZRANGEBYSCORE(now, +inf)` 一次查出所有**未过期**的平台——在线判断
2. `ZREMRANGEBYSCORE(-inf, now)` 一次清掉所有**已过期**的成员——垃圾回收
3. `ZADD` 更新某个平台的 score——续期（不改变成员数量）

三个操作都是 O(log n)，而且**按时间范围查询**是 ZSet 的原生能力。用 Hash 或 String 都做不到。

> ⚠️ **一个已有的 bug**（15 篇提过，这里从存储角度重述）：
> ```go
> // online.go:66
> strValues, err := s.rdb.ZRange(ctx, key, 0, -1).Result()   // ← 没有过滤 score
> ```
> `GetAllOnlineUsers` 用的是 `ZRange`（全量），**不过滤过期的 score**。而 `GetOnline` 用的是 `ZRangeByScore`（过滤）。
>
> 后果：如果一个用户的成员全过期了但 key 还没被删（`EXPIRE` 是 30 分钟，成员也可能过期但没清），`GetAllOnlineUsers` 会**认为他还在线**。
>
> 修法：把 `ZRange` 改成 `ZRangeByScore(now, +inf)`，和 `GetOnline` 口径一致。这是「**同一语义两种实现**」的经典 bug。

## 2.6 Redis 三种部署模式

`config/redis.yml`：

```yaml
address: [localhost:16379]
redisMode: "standalone"      # standalone / cluster / sentinel
db: 0
maxRetry: 10
poolSize: 100
sentinelMode:
  masterName: "redis-master"
  sentinelsAddrs: ["127.0.0.1:26379", "127.0.0.1:26380", "127.0.0.1:26381"]
  routeByLatency: true
  routeRandomly: true
```

三种模式的实现（外部模块 `redisutil/redis.go`）：

| 模式 | 客户端类型 | 特点 |
|---|---|---|
| `standalone` | `redis.NewClient` | 单机，DB 可以选择 |
| `sentinel` | `redis.NewFailoverClusterClient` | 哨兵自动选主，`routeByLatency` 按延迟路由 |
| `cluster` | `redis.NewClusterClient` | 16384 槽分片，`db` 必须是 0（集群不支持多 DB） |

**为什么 `redisMode` 是字符串而不是 booleans？** 因为三种模式**互斥**，用枚举比多个 bool 清晰。这是一个小的设计经验：**互斥的开关用枚举，独立的开关用 bool**。

> 🔴 **注意 `db: 0` 这个细节**：Redis Cluster **只支持 DB 0**。所以如果要从 standalone 迁到 cluster，代码里所有假设「db 可以选」的地方都要改。OpenIM 默认 `db: 0`，算是提前规避了。

## 2.7 Redis 与三层缓存的位置关系

这一节是全篇的「地图」，把 Part5 的四篇串起来：

```
        请求
         ↓
┌─────────────────────────────────────┐
│ L1: 进程内 LRU 缓存（pkg/localcache）│  ← 24 篇
│     · 缓存的是 RPC 响应（[]byte）    │
│     · 500 槽 × 20000 条，TTL 5 分钟  │
│     · 命中率最高的层，纳秒级         │
└─────────────────────────────────────┘
         ↓ 未命中
┌─────────────────────────────────────┐
│ L2: Redis（rockscache）              │  ← 本篇
│     · 缓存的是数据库记录（JSON）      │
│     · HASH 三字段 + 锁 + 延迟删      │
│     · 毫秒级（网络往返）             │
└─────────────────────────────────────┘
         ↓ 未命中
┌─────────────────────────────────────┐
│ L3: MongoDB                          │  ← 21 篇
│     · 真相                             │
│     · 十毫秒级                       │
└─────────────────────────────────────┘
```

**为什么需要 L1？** Redis 已经很快了（10 万 QPS），为什么还要本地缓存？

因为**网络往返**。即使是同机房的 Redis，一次 `HGET` 也要 0.2-0.5ms。而本地 LRU 是 100ns 级别——**差 1000 倍**。

而且 IM 的读放大很严重：发一条群消息，要校验「发送者是否在群」「群是否被禁言」「接收者有几人」，每个校验都是一次 RPC，每次 RPC 又要查缓存。**几万条消息/秒 × 每个消息几次校验 = 几十万次缓存查询**。这些全打到 Redis 上，网络带宽和 Redis 单线程都会成为瓶颈。

L1 的价值就在这里：**把最热的数据（用户信息、群信息、是否好友）放在进程内存里，根本不走网络**。

**代价是「多副本不一致」**——12 个进程各有自己的 L1，A 进程更新了数据，B 进程的 L1 还是旧的。这就必须有一套**失效广播机制**（Pub/Sub），这是 24 篇的核心。

> 🔴 **三级缓存的完整一致性方案**：
> 1. 写数据 → 更新 Mongo
> 2. → `TagAsDeleted` 标记 Redis 失效（延迟过期）
> 3. → `PUBLISH DELETE_CACHE_USER [keys]` 广播
> 4. → 所有节点收到消息，`DelLocal` 删本地 L1
> 5. → 下一次读：L1 miss → L2 miss → 回源 Mongo → 逐层回填
>
> **注意：这仍然不是强一致**。在「写 Mongo」和「广播到达」之间（约 1ms），其他节点可能读到旧值。IM 接受这个窗口，因为消息本身有 seq 保证顺序，短暂读到旧的好友状态不会导致数据错误。

---

# 第三部分　实验

### 🧪 实验 22-1：看真实的 key 长什么样

```bash
docker exec -it redis redis-cli -a <密码> -n 0

# 1. 看有哪些 key（生产环境别用 keys *，这里数据少可以用）
KEYS *

# 2. 验证 rockscache 的 HASH 结构
TYPE USER_INFO:imAdmin
HGETALL USER_INFO:imAdmin
# 预期看到 value / lockUntil / lockOwner 三个字段

# 3. 验证 token 的 HASH（field 是完整 JWT）
TYPE UID_PID_TOKEN_STATUS:imAdmin:ios
HGETALL UID_PID_TOKEN_STATUS:imAdmin:ios

# 4. 验证 online 的 ZSET（member 是平台，score 是到期时间戳）
TYPE ONLINE:imAdmin
ZRANGE ONLINE:imAdmin 0 -1 WITHSCORES
# score 应该是一个 10 位数（Unix 时间戳），且小于当前时间说明已过期

# 5. 验证 seq 的 HASH（12 篇的号段）
HGETALL MALLOC_SEQ:si_imAdmin_<someone>
```

**重点看第 2 步**：如果你看到 `value`/`lockUntil`/`lockOwner`，就证明 rockscache 的 HASH 三字段设计是真的，不是我编的。

### 🧪 实验 22-2：手动制造一次「缓存击穿 + 延迟删」

```bash
# 1. 先让缓存存在
curl -s -X POST "http://127.0.0.1:10002/user/get_users_info" \
  -H "Content-Type: application/json" -H "operationID: t1" -H "token: <你的token>" \
  -d '{"userIDs":["imAdmin"]}'

# 2. 看 TTL
TTL USER_INFO:imAdmin       # 应该是 ~43000 秒（12 小时）

# 3. 手动标记删除（模拟 TagAsDeleted）
HSET USER_INFO:imAdmin lockUntil 0
EXPIRE USER_INFO:imAdmin 3

# 4. 立刻再请求一次（会触发回源）
curl -s -X POST "http://127.0.0.1:10002/user/get_users_info" ... 

# 5. 看 TTL 和 lockUntil
TTL USER_INFO:imAdmin       # 恢复成 12 小时了
HGET USER_INFO:imAdmin lockUntil   # 字段被删了（nil）
```

**这个实验让你亲眼看到**：缓存不是「被删掉再重建」，而是「标记失效 → 抢锁 → 回源 → 覆盖」。

### 🧪 实验 22-3：观察 Pub/Sub 广播

```bash
# 开一个终端订阅
docker exec -it redis redis-cli -a <密码> SUBSCRIBE DELETE_CACHE_USER

# 另一个终端改一个用户的昵称
curl -s -X POST "http://127.0.0.1:10002/user/update_user_info" \
  -H "Content-Type: application/json" -H "token: <token>" -H "operationID: t1" \
  -d '{"userInfo":{"userID":"imAdmin","nickName":"新昵称","faceURL":""}}'

# 第一个终端应该看到一条消息，payload 是 ["USER_INFO:imAdmin"]
```

**能看到广播，就理解了 L1 缓存怎么跨进程失效**。

---

# 第四部分　自测题

1. **Redis 单线程为什么快？** 说出四个原因，并解释「单线程的代价是什么」。
2. **为什么跳表比红黑树更适合 ZSet？** 三个理由。
3. **`HSET` 的字段级原子性，对 rockscache 的设计有什么影响？** 如果只能存 String，该怎么办？
4. **缓存穿透、击穿、雪崩，三者的区别是什么？** 各自的标准解法。
5. **OpenIM 为什么要区分 `successExpire` 和 `failedExpire`？** 只用一个 TTL 会怎样？
6. **「先写库再删缓存」为什么比「先删缓存再写库」安全？** 画出两种方案的最坏时序。
7. **rockscache 的 `luaSet` 里，为什么必须校验 `lockOwner`？** 不校验会发生什么？
8. **`TagAsDeleted` 为什么不直接 `DEL`，而是 `HSET lockUntil 0` + `EXPIRE Delay`？**
9. **Redis Cluster 为什么要求批量操作的 key 在同一槽？** `ProcessKeysBySlot` 怎么解决的？
10. **为什么 seq 类的 key 不能设 TTL，而业务缓存必须设 TTL？** 用「缓存 vs 状态」来解释。
11. **token 的 value 为什么是 "0"/"1" 而不是直接删除 field？**
12. **三级缓存中，L1 的价值是什么？** 用具体数字说明。

---

# 第五部分　面试话术

**问：你们的 Redis 用来做什么？**

> 「四件事，不只是缓存。
> 第一是**缓存**：好友、群、会话、用户这些读多写少的数据，用 rockscache 管，这个库的设计挺讲究，缓存 key 是 HASH，存 value + lockUntil + lockOwner 三个字段，靠 Lua 做原子抢锁。
> 第二是**状态**：token 白名单、seq 水位、在线状态。这些不是缓存——它们丢了要出大事，所以 seq 类的 key 刻意不设 TTL。
> 第三是**分布式锁**：seq 分配器的号段锁，还有 rockscache 的缓存刷新锁。
> 第四是**广播**：用 Pub/Sub 做本地缓存的失效通知，五个业务域各一个 channel。」

**追问：缓存一致性你们怎么做的？**

> 「本质上是 Cache Aside 加延迟删除。写数据的流程是：先更 Mongo，然后调 `TagAsDeleted`。
> 这里 `TagAsDeleted` 不是 `DEL`，而是把 `lockUntil` 置 0、把 TTL 改成延迟时间。这个设计的用意是——传统 `DEL` 的问题是『删掉之后的第一个读请求会回填旧值』，而标记成可抢锁状态等于告诉后续所有请求『这个值可能脏了，谁抢到锁谁去刷新』。
> 读数据的时候走 strongFetch：如果缓存被锁着，就 Sleep 等；抢到锁的请求去查库，写回时必须校验 `lockOwner` 还是不是自己——这防止了『锁过期后被别人抢走，旧请求回来覆盖新值』。
> 另外 TTL 加了 20% 的随机抖动，防雪崩。」

**追问：为什么不直接上分布式事务？**

> 「因为跨系统。发一条消息要写 Redis 的 seq、写 Mongo 的消息、发 Kafka 的推送，三个不同系统，没有一个事务能覆盖。
> 我们的策略是**每一环都幂等**：Mongo 用 `doc_id` 唯一索引 + 先 update 后 insert 保证幂等；Kafka 消费失败就不提交位点，重投后还是幂等；seq 是 Redis 里单调递增的号段，重复分配只会有空洞，不会重复。
> **幂等比事务便宜得多，而且能覆盖跨系统场景。**」

**追问：你觉得你们缓存这块有什么问题？**

> 「我找到三个。
> 第一个是 `online.go:66` 的 `GetAllOnlineUsers` 用 `ZRange` 全量取，没过滤 score，而 `GetOnline` 用的是 `ZRangeByScore` 过滤过期。两处对『在线』的判定口径不一致，会导致已经下线的人被当成在线。
> 第二个是 `CONVERSATION:` 和 `CONVERSATION_IDS:` 前缀有包含关系，如果哪天用 `SCAN CONVERSATION:*` 批量清理会误伤。
> 第三个是 `lua_script.go` 里那 127 行自研的批量 Lua 函数完全没人调用，只有测试在调。应该是后来换了 rockscache 的 `FetchBatch2`，但旧代码没删。」

---

> **下一篇**：23 Kafka 与消息可靠性。会讲清为什么需要 MQ、分区内有序怎么保证、at-least-once 的位点到底什么时候提交，以及 OpenIM 的 mq 抽象层怎么在 Kafka / Redis Stream / 内存队列之间切换。
