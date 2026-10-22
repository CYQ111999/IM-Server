# 23 Kafka 与消息可靠性

> **本篇定位**：Part5 第三篇。21 篇讲「数据落在哪」，22 篇讲「缓存」，本篇讲「**中间那段异步管道**」——消息怎么从发送方流到落库方、推送方，以及为什么这个管道能保证不丢。
> **前置**：10（发送链路）、11（消息队列与中转，已讲过 batcher 和 topic 分工，本篇从 MQ 原理角度重讲）。
> **阅读方式**：第一部分纯理论（Kafka 的模型、有序性、投递语义）；第二部分是本仓库的 mq 抽象层、三种引擎、以及 at-least-once 到底落在哪一行。
> **本篇标记**：🔴 重点　🟡 难点　⚠️ 易错　🧪 实验　📌 待验证

---

# 第一部分　理论：消息队列到底在保证什么

## 1.1 为什么需要 MQ：三个理由，只有一个是真的

教科书上写 MQ 有三大作用：解耦、异步、削峰。听起来都对，但用在一个具体项目里，通常**只有一个是真正的动力**。OpenIM 的是哪一个？

**解耦？** 部分成立。发送消息的服务不需要知道「谁在消费」，加一个新消费者（比如「消息审计」）不用改发送方。但 OpenIM 只有固定的四个 topic，消费者是写死的，解耦的收益不大。

**异步？** 成立，但不是核心。为了让 `SendMsg` 快速返回，把落库和推送放到后台——用 goroutine 也能做到（内存队列 + 后台协程）。不一定需要 Kafka。

**削峰？才是真正的理由。** 算一笔账：

```
高峰 5 万条消息/秒
MongoDB 单文档写入 ≈ 1ms（含网络往返 + 索引更新）
按「一条消息一个文档」写 → 需要 5 万个并发写
MongoDB 单机撑不住（连接池 100，队列积压到爆炸）
```

用 Kafka 之后：

```
发送侧：5 万条/秒 全部丢进 Kafka（Kafka 单机可以到几十万条/秒，顺序写磁盘）
消费侧：msgtransfer 以自己舒服的速度消费（比如 5000 条/秒）
积压：Kafka 里堆着，磁盘扛着
```

**关键洞察**：MQ 把「生产速度」和「消费速度」解耦了。生产快的时候积压在 MQ 里（磁盘），而不是压在内存里（OOM）或压在数据库上（超时）。

> 🔴 **这句话能直接用在面试里**：「我们用 MQ 不是为了解耦，是为了削峰。峰值 5 万 QPS 的消息写入，MongoDB 直接承受会打满连接池；放进 Kafka 之后，消费端可以按自己的能力匀速消费，积压落在磁盘上而不是内存里。」

## 1.2 Kafka 的核心模型：五个概念

先建立一张图：

```
                    ┌─────────── Topic: toRedis（逻辑概念）───────────┐
                    │                                                  │
Producer ──key──→  ┌─┴─ Partition 0 ─→ [msg0][msg1][msg2][msg3]...    │ ← offset 递增
                   ├── Partition 1 ─→ [msg0][msg1]...                  │
                   ├── Partition 2 ─→ [msg0][msg1][msg2]...            │
                   └── ...（共 8 个，见 KAFKA_NUM_PARTITIONS）
                    │                                                  │
                    └──────────────────────────────────────────────────┘
                                     ↑
                          Consumer Group "redis"
                          （组内每个 partition 只被一个消费者消费）
```

### Topic：逻辑分类

`toRedis`、`toMongo`、`toPush`、`toOfflinePush` 是四个 topic，对应四条独立的管道。

**注意**：Topic 只是个名字，**数据实际存在 Partition 里**。

### Partition：并行度的单位

一个 topic 分成 N 个 partition（OpenIM 配的是 8）。

- **partition 是 Kafka 的最小并行单位**：一个 partition 同一时刻只能被消费组里的一个消费者消费
- 所以「8 个 partition + 1 个消费者」= 消费并行度 1（一个消费者要轮流处理 8 个 partition）
- 「8 个 partition + 8 个消费者」= 并行度 8

**这就是为什么 partition 数决定了消费端能扩到几个实例**。OpenIM 配 8，意味着 msgtransfer 最多扩到 8 个实例（再多就有的实例分不到 partition，白占资源）。

> 🟡 **一个常见的容量规划错误**：业务量涨了 10 倍，把 msgtransfer 从 4 个扩到 40 个，结果吞吐量没涨。因为 partition 只有 8 个，多出来的 32 个实例全在空转。**扩实例之前先确认 partition 数够不够**。

### Offset：消费者在 partition 里的位置

每个 partition 里的消息按写入顺序编号：0, 1, 2, 3...

**offset 是消费者自己维护的「我读到哪了」**，不是消息自带的属性。同一个 partition，消费者 A 的 offset 是 100，消费者 B（另一个消费组）的 offset 可能是 50——**互不影响**。

### Consumer Group：消费组

同一个组内的消费者**分摊** partition，不同组之间**各自全量消费**。

```
toRedis topic：
  组 "redis" 的 msgtransfer 实例 A、B、C  → 分摊 8 个 partition
  组 "audit" 的审计服务 D                → 也能读到全部消息
```

**组内是队列语义（一条消息只被组内一个消费者处理），组间是广播语义（每个组都收到全量）**。这个二义性是 Kafka 最容易被问倒的地方。

### Rebalance：分区再平衡

消费者加入/退出组时，partition 要重新分配。触发条件：
- 新消费者加入
- 消费者退出（崩溃、滚动重启）
- 订阅的 topic 变化

**Rebalance 期间整个组停止消费**（Stop-the-world）。这是 Kafka 的一个痛点，也是「滚动发布时消费会短暂停止」的原因。

> ⚠️ **OpenIM 的 `RegisterHandleAndConsumer` 会重连**（`pkg/common/storage/kafka/consumer_group.go:37`）：
> ```go
> for {
>     err := mc.ConsumerGroup.Consume(ctx, mc.topics, handler)
>     if errors.Is(err, sarama.ErrClosedConsumerGroup) { return }
>     if errors.Is(err, context.Canceled) { return }
>     if err != nil { log.ZWarn(ctx, "consume err", err, ...) }
> }
> ```
> 但**这段代码在业务链路上没人用**（见 2.8 节），实际走的是外部模块的另一套实现。

## 1.3 分区内有序：最有用的一个保证

Kafka 只保证 **partition 内有序**，不保证 topic 全局有序。

这个限制看起来很弱，但**足够解决 IM 的核心问题**——因为 IM 需要的不是「全局有序」，是「**同一个会话内有序**」。

```
会话 A 的消息 → 都发到 partition 3  → 有序
会话 B 的消息 → 都发到 partition 5  → 有序
A 和 B 之间不需要有序
```

**实现方式：用会话 ID 做消息的 key。**

```go
// pkg/common/storage/kafka/producer.go:43-47
kMsg := &sarama.ProducerMessage{
    Topic: p.topic,
    Key:   sarama.StringEncoder(key),    // ← 会话 ID
    Value: sarama.ByteEncoder(bMsg),
}
```

```go
// pkg/common/storage/kafka/sarama.go:45
kfk.Producer.Partitioner = sarama.NewHashPartitioner   // ← 按 key 哈希选分区
```

`HashPartitioner` 的逻辑是 `hash(key) % numPartitions`。**同一个 key 永远落到同一个 partition**（前提是 partition 数不变）。

> 🔴 **为什么这一条是「保序的第一层防线」？**
> 因为整个消息链路有三段可能乱序的地方：
> 1. **Kafka 内部**：同一 partition 内天然有序 ✅
> 2. **消费端分发到 worker**：OpenIM 按 key 哈希分片（`batcher.Sharding`），同一会话永远同一个 worker ✅
> 3. **worker 内部并发处理**：同一 key 的消息被聚合成一个批次（`batcher.Key`），串行处理 ✅
>
> **三层防线缺一不可**。只做第一层，消费者内部并发就乱了；只做第二层，Kafka 里就乱了。11 篇详细讲过这三层，这里从「为什么第一层必须用 key 哈希」的角度补一句：**如果用 RoundRobin 分区器，同一个会话的消息会被打散到 8 个 partition，无论消费端怎么做都救不回来**。

> ⚠️ **一个真实的 bug**（15 篇发现的，从 MQ 角度看更清楚）：
> ```go
> // internal/push/push_handler.go:276
> key := GenConversationUniqueKeyForSingle(val.SendID, val.RecvID)
> ```
> 群聊消息的 `RecvID` 是空的，所以**所有群聊消息的 key 都是 `sender_`**。哈希后全落到同一个 partition → **8 个 partition 里 7 个闲置，1 个被打爆**。
>
> 正确做法是用 `val.ConversationID` 做 key（群聊时是 `sg_groupID`）。这个 bug 的影响是「群聊的离线推送全部串行」，高峰期会严重积压。

## 1.4 三种投递语义

这是消息队列最核心的概念，也是面试必问。

| 语义 | 含义 | 怎么实现 | 代价 |
|---|---|---|---|
| **at-most-once** | 最多一次（可能丢） | 先提交位点，再处理 | 丢消息 |
| **at-least-once** | 至少一次（可能重） | 先处理，处理完再提交位点 | 重复消息 |
| **exactly-once** | 恰好一次 | 事务 + 幂等（Kafka 0.11+ 的幂等生产者 + 事务） | 复杂、慢 |

### 为什么 exactly-once 这么难

「恰好一次」需要保证两件事同时成立：
1. 消息**不丢**（处理完才提交位点）
2. 消息**不重**（提交位点和处理结果必须原子）

第二点是难点：处理结果写在 MongoDB，位点写在 Kafka，**两个不同系统**。要么用分布式事务（极慢），要么让「重复处理」变得无害（**幂等**）。

Kafka 的 exactly-once 只在「Kafka → Kafka」的流处理场景（比如 Kafka Streams）里真正成立。**跨系统（Kafka → MongoDB）的 exactly-once 是不存在的**，只能用 at-least-once + 幂等来模拟。

> 🔴 **面试时不要吹「我们用了 exactly-once」**。正确的说法是：「我们用的是 at-least-once + 幂等消费，效果等价于 exactly-once」。这个措辞既准确又显得你懂。

### at-least-once 的两个落点

「至少一次」的实现取决于**位点什么时候提交**：

```
位置 A：收到消息就提交位点 → 处理失败就丢了 → at-most-once
位置 B：处理成功才提交位点 → 处理失败会重投 → at-least-once ✅
```

OpenIM 用 B，具体在 `batcher` 的 `OnComplete`（11 篇讲过，这里给出完整链路）：

```go
// pkg/tools/batcher/batcher.go:247-258
func (b *Batcher[T]) distributeMessage(messages map[string][]*T, totalCount int, lastMessage *T) {
    triggerID := idutil.OperationIDGenerator()
    b.HookFunc(triggerID, messages, totalCount, lastMessage)
    for key, data := range messages {
        if b.config.syncWait {
            b.counter.Add(1)                       // ① 计数 +1
        }
        channelID := b.Sharding(key)
        b.chArrays[channelID] <- &Msg[T]{...}      // ② 分发到 worker
    }
    if b.config.syncWait {
        b.counter.Wait()                           // ③ 等所有 worker 处理完
    }
    if b.OnComplete != nil {
        b.OnComplete(lastMessage, totalCount)      // ④ 才提交位点
    }
}
```

```go
// pkg/tools/batcher/batcher.go:262-278
func (b *Batcher[T]) run(channelID int, ch <-chan *Msg[T]) {
    for {
        select {
        case messages, ok := <-ch:
            if !ok { return }
            b.Do(ctx, channelID, messages)          // ⑤ 真正的业务处理
            if b.config.syncWait {
                b.counter.Done()                    // ⑥ 处理完才减计数
            }
        }
    }
}
```

```go
// internal/msgtransfer/online_history_msg_handler.go:103-106
och.redisMessageBatches.OnComplete = func(lastMessage *ConsumerMessage, totalCount int) {
    lastMessage.Raw.Mark()      // ⑦ 标记「这条消息处理完了」
    lastMessage.Raw.Commit()    // ⑧ 提交位点到这条消息之后
}
```

**为什么是 `lastMessage` 而不是全部 Mark？**

因为一批消息在 Kafka 里是**连续的 offset**。标记最后一条，等于声明「这条以及它前面的都处理完了」。这是 Kafka 位点语义（commit offset = 「下一条要读的位置」）的用法约定。

> 🔴 **这里有个隐含前提，redismq 的代码注释把它写得非常清楚**（`tools/mq/redismq/acker.go:14-28`）：
>
> ```
> CONTRACT (same one Kafka offset-commit relies on): marking id X must mean
> every delivered id <= X is also done.
> ```
>
> 翻译：**标记 X 就意味着所有 ≤ X 的都完成了**。这个前提之所以成立，是因为：
> 1. 一个 partition 按顺序投递
> 2. `syncWait=true` 保证这批所有 worker 都跑完才 `OnComplete`
> 3. 所以「最后一条被 Mark」时，前面的一定也完成了
>
> **如果哪天有人把 `syncWait` 改成 false**（异步处理，不等 worker），这个前提就破了——最后一条先完成、前面的还在跑，Mark 就把位点推到前面去了，中间的消息丢了。这是**一个配置项和整个可靠性契约绑死**的例子。

### at-least-once 的代价：必须幂等

既然会重复投递，消费端必须能「重复处理同一个消息而不出错」。

OpenIM 的三个幂等点：

1. **消息落库**：`doc_id` 唯一索引 + 「先 update 后 insert」（13 篇）
2. **seq 分配**：号段预分配，重复分配只产生空洞，不会重复
3. **会话更新**：`$max` 操作符（只在更大时更新），不是 `$set`

> 🟡 **`$max` 是一个容易被忽略的幂等利器**：
> ```javascript
> db.conversation.updateOne({_id: ...}, {$max: {max_seq: 100}})
> ```
> 重复执行 100 次，结果都一样（max_seq 不会变小）。而 `$set` 就会出问题——如果乱序到达（先处理了 seq=100，又处理了 seq=50），`$set` 会把 max_seq 改成 50。

## 1.5 消费者什么时候会「丢消息」

这是可靠性分析的实战清单。按可能性排序：

### ① 位点提交了，但业务没成功

```go
// 典型错误写法
func handler(msg) error {
    err := doSomething(msg)
    msg.Mark()
    msg.Commit()      // ← 不管 err，总是提交
    return nil
}
```

OpenIM 的 `HandleChatWs2Mongo` **做对了一半**（`online_msg_to_mongo_handler.go:43-50`）：

```go
err = mc.msgTransferDatabase.BatchInsertChat2DB(ctx, ...)
if err != nil {
    log.ZError(...)
    prommetrics.MsgInsertMongoFailedCounter.Inc()
} else {
    prommetrics.MsgInsertMongoSuccessCounter.Inc()
    val.Mark()          // ← 只在成功时 Mark
}
```

**只在成功时 Mark**——这是对的。但问题在于**上层不知道它失败了**：

```go
// internal/msgtransfer/init.go:128-131
fn := func(msg mq.Message) error {
    m.historyMongoHandler.HandleChatWs2Mongo(msg)
    return nil          // ← 永远返回 nil
}
```

`HandleChatWs2Mongo` 没有返回值，`fn` 永远返回 nil。所以消费者认为「处理成功」，继续处理下一条。

**后果**：消息 1 落库失败（不 Mark），消息 2 落库成功（Mark + Commit）。Kafka 提交的位点是「消息 2 之后」，下次从「消息 2 之后」开始消费——**消息 1 永远丢了**。

> ⚠️ **这是一个真实的丢消息路径**，不是理论推演。修法是把 `HandleChatWs2Mongo` 改成返回 `error`，失败时 `fn` 返回 error，让消费者的 `Subscribe` 返回错误，从而不提交位点（下次重投）。
>
> 但这样会引入另一个问题：如果这条消息**永远处理不成功**（比如数据格式错误），会无限重投，卡死整个 partition。所以生产上通常要有「死信队列」或「重试 N 次后跳过」的策略。**OpenIM 两者都没有**。

### ② 消费者崩溃，位点已提前提交

自动提交（`autoCommitEnable=true`）的行为是：**每 5 秒自动提交一次当前已 Mark 的最大 offset**。

```
t0: 收到 msg 1..100，全部 Mark
t1: 定时任务提交 offset=101
t2: 正在处理 msg 101（还没落库）
t3: 进程崩溃
→ 重启后从 101 开始，msg 101 丢了
```

自动提交的窗口是「提交间隔」——**最多丢 5 秒内处理的消息**。

**这不算 bug，是 at-most-once 的一种退化**。要彻底避免只能关掉自动提交，手动 `Commit`（OpenIM 的做法，`syncWait=true` + `OnComplete` 里显式 Commit）。

📌 **待验证**：`mqbuild` 传的是 `autoCommitEnable=true`（`builder.go:172`），同时 `OnComplete` 里又手动 `Commit()`。两者并存——自动提交是兜底（防止长时间不提交导致位点落后），手动提交保证精确性。但**自动提交可能在不该提交的时候提交**（比如 worker 正在处理时定时器触发）。这个组合是否安全，需要看 sarama 对「手动 Mark + 自动 Commit」的处理。**建议实测**（用 22 篇的实验方法，改一条消息让落库失败，观察是否重投）。

### ③ 消息在 producer 端就丢了

```go
// pkg/common/storage/kafka/sarama.go:51-60
switch strings.ToLower(conf.ProducerAck) {
case "no_response":
    kfk.Producer.RequiredAcks = sarama.NoResponse       // ← 丢消息风险最高
case "wait_for_local":
    kfk.Producer.RequiredAcks = sarama.WaitForLocal     // 只有 leader 确认
case "wait_for_all":
    kfk.Producer.RequiredAcks = sarama.WaitForAll       // ISR 全部确认
default:
    kfk.Producer.RequiredAcks = sarama.WaitForAll       // ← 默认最安全
}
```

三种 ack 级别：

| 级别 | 含义 | 风险 |
|---|---|---|
| `NoResponse` | 发出去就不管 | leader 挂了 → 丢 |
| `WaitForLocal` | 等 leader 写入 | leader 写入后挂了、还没同步到从 → 丢 |
| `WaitForAll` | 等所有 ISR（in-sync replica）写入 | 最安全，但慢 |

**OpenIM 的 `config/kafka.yml` 里 `producerAck:` 是空的**，所以走 `default` 分支 = `WaitForAll`。

> 🔴 **这是一个好的默认值**。很多项目为了性能改成 `WaitForLocal`，然后在故障时丢消息。**消息系统的默认值应该偏安全**。

### ④ 单条消息超过 max.message.bytes

Kafka 默认单条消息上限 1MB。OpenIM 的消息是 protobuf 序列化后的 `MsgData`，如果带大图片的 base64 内容，**很容易超**。

OpenIM 的应对：**大文件走对象存储**（20 篇），消息里只放 URL。所以正常消息都很小。

但如果客户端的 `Ex` 或 `offlinePushInfo` 塞了大 JSON，还是可能超。📌 **待验证**：`docker-compose.yml` 里没有配 `KAFKA_CFG_MESSAGE_MAX_BYTES`，用的是默认值。

## 1.6 消费积压了怎么办

这是一个运维问题，但面试常问「你们怎么处理消息积压」。

**先诊断，再处理**。四种情况：

| 现象 | 原因 | 处理 |
|---|---|---|
| 所有 partition 都积压 | **消费能力不足** | 扩消费者（受 partition 数限制）或优化单条处理耗时 |
| 只有个别 partition 积压 | **key 分布不均** | 换 key（比如 15 篇那个用 `SendID_RecvID` 做群消息 key 的 bug） |
| 积压突然暴涨后回落 | 正常峰值 | 不用管，MQ 就是干这个的 |
| 积压持续增长不回落 | 消费卡死 | 看是不是某条消息一直失败在重试 |

**扩消费者的上限是 partition 数**。OpenIM 配 8 个 partition，msgtransfer 最多有效扩到 8 个实例。

> 🟡 **一个非常规手段：临时扩 partition**。`kafka-topics.sh --alter --partitions 16`。但注意：**扩 partition 会破坏「同 key 同 partition」的约定**——扩之前 hash 到 3 的 key，扩之后可能 hash 到 9。如果此时还有积压消息在旧 partition 里没消费完，就会出现「同一会话的新消息在 partition 9，旧消息在 partition 3」→ **乱序**。
>
> 所以扩 partition 要在**积压清零后**做。这是一个很实战的坑。

## 1.7 MQ 抽象层：为什么 OpenIM 要包一层

OpenIM 没有直接用 sarama，而是在 `github.com/openimsdk/tools/mq` 里定义了一套接口：

```go
// tools/mq/mq.go（外部模块）
type Message interface {
    Context() context.Context
    Key() string
    Value() []byte
    Mark()      // 标记这条处理完成
    Commit()    // 提交位点
}

type Handler func(msg Message) error

type Consumer interface {
    Subscribe(ctx context.Context, fn Handler) error
    Close() error
}

type Producer interface {
    SendMessage(ctx context.Context, key string, value []byte) error
    Close() error
}
```

**只有 20 行，但抽象得很到位**。关键设计：

1. **`Message` 只有 `Key`/`Value`/`Context`**：不暴露 partition、offset 这些 Kafka 特有的概念。换成 Redis Stream 时，`Key` 是 message ID，`Value` 是内容，同样适用。
2. **`Mark` 和 `Commit` 分开**：因为 Redis Stream 的语义不同（每条消息单独 XACK，没有「批量 offset 提交」）。分开之后，两种 MQ 都能实现。
3. **`Consumer.Subscribe` 是阻塞的，每次处理一条**：这是最简单也最通用的模型。

> 🔴 **抽象层的价值就在这里**：三种 MQ 引擎（Kafka / Redis Stream / 内存）**对上层完全透明**。`internal/msgtransfer` 的代码里没有任何 `sarama` 的 import。

**但抽象是有代价的**：丢掉了 Kafka 的高级特性。比如：
- 不能控制 `MaxProcessingTime`（Rebalance 超时）
- 不能自己管 partition 分配策略
- `Subscribe` 一次一条，**没有 `ConsumeClaim` 那样的批量拉取优化**

## 1.8 三种引擎怎么选

`config/share.yml`：

```yaml
queue: kafka      # 可选 kafka / redis / memory
```

```go
// pkg/mqbuild/builder.go:47-63
func NewBuilder(queue config.EngineSelector, kafka *config.Kafka, redis redis.UniversalClient) (Builder, error) {
    engine, err := config.ValidateQueueEngine(queue.Engine, config.Standalone())
    switch engine {
    case config.QueueEngineKafka:
        return newKafkaBuilder(kafka), nil
    case config.QueueEngineRedis:
        return newRedisBuilder(redis), nil
    case config.QueueEngineMemory:
        return standaloneBuilder{}, nil
    }
}
```

| 引擎 | 实现 | 适用场景 |
|---|---|---|
| `kafka` | 外部模块 `mq/kafka`（sarama 封装） | **生产**：真正的削峰、持久化、多消费者 |
| `redis` | 外部模块 `mq/redismq`（Redis Stream） | **小规模**：不想额外维护 Kafka |
| `memory` | 外部模块 `mq/simmq`（进程内 channel） | **仅 standalone**：本地开发，零依赖 |

**`memory` 只能 standalone 用**（`ValidateQueueEngine` 会校验）：

```go
// simmq 的实现（tools/mq/simmq/global.go）
var topicMq map[string]*memory      // 全局 map，key 是 topic 名
```

因为它是**进程内**的：12 个服务在一个进程里，可以用同一个全局 map 通信；如果拆成 12 个进程，各自的内存队列互不相通，消息就丢了。

> 🔴 **这个限制暴露了「standalone 模式」的本质**：它不是「微服务的简化版」，而是「**用一个进程模拟 12 个服务**」。所以低耦合的部分（gRPC 调用）可以用 inprocess 发现，但**必须跨进程的部分（MQ）** 就不能用内存版。
>
> 这也是为什么 `components/kafka`（1.7GB 数据）不能删——**standalone 模式也真的在用 Kafka**。

---

# 第二部分　源码：从 topic 到 Mark()

## 2.1 四个 topic 与四个消费组

```go
// pkg/mqbuild/builder.go:25-38
const (
    TopicToRedis       = "toRedis"
    TopicToMongo       = "toMongo"
    TopicToPush        = "toPush"
    TopicToOfflinePush = "toOfflinePush"
)

const (
    GroupRedis       = "redis"
    GroupMongo       = "mongo"
    GroupPush        = "push"
    GroupOfflinePush = "offlinePush"
)
```

`config/kafka.yml` 可以覆盖：

```yaml
toRedisTopic: toRedis
toMongoTopic: toMongo
toPushTopic: toPush
toOfflinePushTopic: toOfflinePush
toRedisGroupID: redis
toMongoGroupID: mongo
toPushGroupID: push
toOfflinePushGroupID: offlinePush
```

**逻辑名 vs 真实名**：

```go
// builder.go:145-172
type kafkaBuilder struct {
    logicalTopic map[string]string    // "toRedis" → 真实 topic 名
    topicGroupID map[string]string    // 真实 topic 名 → 消费组 ID
}

func (x *kafkaBuilder) GetTopicProducer(ctx context.Context, topic string) (mq.Producer, error) {
    realTopic, ok := x.logicalTopic[topic]
    if !ok {
        return nil, fmt.Errorf("topic %s not found", topic)
    }
    return kafka.NewKafkaProducerV2(x.config, x.addr, realTopic)
}
```

为什么要这一层映射？因为**业务代码里用的是逻辑名 `mqbuild.TopicToRedis`，而实际 topic 名可配置**。这样测试环境可以配成 `toRedis_test`，同一套代码不用改。这是一个小而实用的设计。

### 四个 topic 的数据形态

| Topic | Producer | Consumer | Payload |
|---|---|---|---|
| `toRedis` | `rpc/msg/send.go` | msgtransfer（historyConsumer） | `MsgDataToMQ`（单条消息 + 会话 ID） |
| `toMongo` | msgtransfer（historyHandler 处理后） | msgtransfer（historyMongoConsumer） | `MsgDataToMongoByMQ`（**一批消息** + ConversationID + LastSeq） |
| `toPush` | msgtransfer | push（pushConsumer） | `PushMsgToPushMQ`（推送任务） |
| `toOfflinePush` | push | push（offlinePushConsumer） | `PushMsgToOfflinePushMQ` |

> 🔴 **注意 `toMongo` 的 payload 是一批**。因为 msgtransfer 已经用 batcher 聚合了 500 条，落库时一次写一批（13 篇的 `$push` 多个元素）。**这是「批量写」优化的传递**：发送侧单条 → 中转侧聚合 → 落库侧批量。

## 2.2 分区与有序的源码证据

Kafka 的 8 个 partition 来自 `docker-compose.yml`：

```yaml
kafka:
  environment:
    KAFKA_NUM_PARTITIONS: 8                  # 新建 topic 的默认分区数
    KAFKA_CFG_AUTO_CREATE_TOPICS_ENABLE: "true"
    KAFKA_CFG_PROCESS_ROLES: controller,broker
    KAFKA_CFG_NODE_ID: 0
    KAFKA_CFG_CONTROLLER_QUORUM_VOTERS: 0@kafka:9093
```

**三个要点**：

1. **`KAFKA_NUM_PARTITIONS: 8`** —— 消费并行度的上限
2. **`AUTO_CREATE_TOPICS_ENABLE: "true"`** —— 第一次发消息时自动建 topic。生产环境通常关掉（避免脏 topic），开发环境开着省事
3. **`PROCESS_ROLES: controller,broker` + `CONTROLLER_QUORUM_VOTERS`** —— 这是 **KRaft 模式**，不需要 ZooKeeper

> 🟡 **KRaft 是什么**：Kafka 2.8 之前依赖 ZooKeeper 存元数据（topic 列表、partition 分配、controller 选举）。3.3 之后 KRaft 模式自己管（把元数据也变成 Kafka 内部的 topic），少维护一个组件。你本地就是单节点 KRaft。

**分区器**：

```go
// pkg/common/storage/kafka/sarama.go:45
kfk.Producer.Partitioner = sarama.NewHashPartitioner
```

> ⚠️ **注意这是本仓库的 `storage/kafka` 包，业务链路不用它**（2.8 节会讲）。业务用的是外部模块的 `mq/kafka`，它也有 `NewHashPartitioner`（`BuildProducerConfig` 的实现是同一份代码拷过去的）。

## 2.3 Producer：三个必配参数

```go
// pkg/common/storage/kafka/sarama.go:41-77（简化）
func BuildProducerConfig(conf Config) (*sarama.Config, error) {
    kfk := sarama.NewConfig()
    kfk.Producer.Return.Successes = true      // ① 同步等确认
    kfk.Producer.Return.Errors = true         // ② 收集错误
    kfk.Producer.Partitioner = sarama.NewHashPartitioner   // ③ 按 key 哈希
    ...
}
```

**为什么 `Return.Successes` 必须是 true？**

因为用的是 **`SyncProducer`**（同步生产者）：

```go
// pkg/common/storage/kafka/producer.go:62
partition, offset, err := p.producer.SendMessage(kMsg)
```

`SyncProducer.SendMessage` 会**阻塞直到 broker 确认**。如果不设 `Return.Successes = true`，sarama 会 panic（"producer.Return.Successes must be true for SyncProducer"）。

**同步生产者的代价**：每次发送至少一次网络往返（~1ms）。5 万 QPS 就需要 5 万并发，不现实。

**那 OpenIM 怎么撑住？** 靠 **`SendMsg` 不在请求链路上**——`SendMsg` 是 RPC 调用（10 篇），客户端发消息走 WS，服务端投 Kafka 是在 goroutine 里做的吗？让我确认一下。

📌 实际上 `rpc/msg/send.go` 的 `SendMsg` 是**同步**投 Kafka：

```go
// pkg/common/storage/controller/msg.go（MsgToMQ）
_, _, err := db.mqProducer.SendMessage(ctx, key, &pbmsg.MsgDataToMQ{...})
```

所以**每条消息的 WS 发送都会等 Kafka 确认**。这是一个性能瓶颈点：单实例的吞吐上限 ≈ 1 / Kafka 往返延迟 ≈ 1000-5000 QPS。

> 🟡 **这是一个真实的架构限制**。要提升只能：
> 1. 用 `AsyncProducer`（异步批量发送），但会丢「发送失败」的反馈
> 2. 在 `SendMsg` 里再套一层内存队列（但那就要处理「内存队列满了怎么办」）
>
> OpenIM 选了最简单可靠的同步方案，**用「一个会话一个 msgtransfer 并行」来扩展**。这是「先保证正确，再优化」的典型取舍。

## 2.4 Consumer：Subscribe 每次只处理一条

外部模块的实现（`tools/mq/kafka/mq_consumer_group.go`）：

```go
// 后台 goroutine 持续从 sarama 拉消息，塞进内部 channel
func (x *mqConsumerGroup) loopConsume() {
    for {
        if err := x.consumer.Consume(x.ctx, x.topics, x); err != nil {
            switch {
            case errors.Is(err, context.Canceled):
                return
            case errors.Is(err, sarama.ErrClosedConsumerGroup):
                return
            }
            log.ZWarn(ctx, "consume err", err, "topic", x.topics, "groupID", x.groupID)
        }
    }
}

// ConsumeClaim 把消息塞进 channel（带宽 64）
func (x *mqConsumerGroup) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
    defer func() { _ = recover() }()      // ← 防止 panic 崩进程
    msg := claim.Messages()
    for {
        select {
        case <-x.ctx.Done():
            return context.Canceled
        case val, ok := <-msg:
            if !ok { return nil }
            select {
            case <-x.ctx.Done():
                return context.Canceled
            case x.msg <- &consumerMessage{Msg: val, Session: session}:
            }
        }
    }
}

// Subscribe 每次只取一条
func (x *mqConsumerGroup) Subscribe(ctx context.Context, fn mq.Handler) error {
    select {
    case <-ctx.Done():
        return context.Cause(ctx)
    case msg, ok := <-x.msg:
        if !ok {
            return sarama.ErrClosedConsumerGroup
        }
        ctx := GetContextWithMQHeader(msg.Msg.Headers)     // ← 恢复链路追踪
        if err := fn(kafkaMessage{ctx: ctx, msg: msg}); err != nil {
            return err
        }
        return nil
    }
}
```

**三层结构**：

```
sarama 的 ConsumeClaim（后台）
    ↓ 塞进 x.msg channel（缓冲 64）
Subscribe（前台，一次取一条）
    ↓ 调 fn
业务 handler
```

**为什么要中间加一个 channel？** 因为 sarama 的 `ConsumeClaim` 是在自己的 goroutine 里跑的，而 `Subscribe` 是业务侧主动调用的。channel 是这两者的**解耦点**。

**`x.msg` 缓冲 64 的意义**：sarama 拉取的消息先堆在 channel 里，业务慢慢处理。64 条之后，`ConsumeClaim` 会阻塞在 `x.msg <- ...`，**触发 sarama 的背压**（不再拉新消息）。

> 🔴 **这是一个「小的背压缓冲」**。它让消费能应对短时的抖动（业务处理慢了几百毫秒），又不会积累太多内存（64 条 × 几 KB = 几百 KB）。

**`GetContextWithMQHeader`** 是链路追踪的关键：

```go
// pkg/common/storage/kafka/util.go:14-34
func GetMQHeaderWithContext(ctx context.Context) ([]sarama.RecordHeader, error) {
    // 把 operationID 等从 context 序列化到 header
}

func GetContextWithMQHeader(header []*sarama.RecordHeader) context.Context {
    // 从 header 恢复 operationID 到 context
}
```

**这解决了「跨 MQ 的链路追踪」**：客户端发消息 → `SendMsg` 生成 operationID → 投 Kafka 时写进 header → 消费端恢复 → 日志里能串起来。

> 🟡 这是**分布式链路追踪的必备能力**。没有它，消息进了 Kafka 就「断链」了，出问题只能从两端分别查。8 篇讲 gRPC 的 metadata 传递时是同一个思路——**上下文要显式传递，不能靠运气**。

## 2.5 Mark 与 Commit 的实现

```go
// tools/mq/kafka/msg.go
func (m kafkaMessage) Mark() {
    m.msg.Session.MarkMessage(m.msg.Msg, "")      // 标记这条消息已处理
}

func (m kafkaMessage) Commit() {
    m.msg.Session.Commit()                        // 提交当前 session 的位点
}
```

**sarama 的语义**：

- `MarkMessage(msg, "")` → 内部调 `MarkOffset(topic, partition, msg.Offset+1, "")`，即「下一条要读的位置」
- `Commit()` → 把 session 里所有已 Mark 的 offset 提交给 broker

**为什么分成两步？** 因为要支持「批量处理、只 Mark 一次」：

```
处理 500 条消息（同一个会话）
→ 只 Mark 最后一条（offset = 1000）
→ Commit
→ 位点 = 1001
```

如果每条都 Mark 再 Commit，就是 500 次网络请求，性能差 500 倍。

> ⚠️ **这个优化的前提就是 1.4 讲的「标记 X 意味着 ≤ X 都完成」**。一旦有人用了 `syncWait=false`（异步），这个前提就破了，而这个破坏是**静默的**——不会报错，只会偶尔丢消息。

## 2.6 redismq 怎么模拟 Kafka 的位点语义

这一节是**全篇最值得读的代码**。Redis Stream 和 Kafka 的语义差异很大：

| | Kafka | Redis Stream |
|---|---|---|
| 位点 | 消费组级别的一个 offset | 每条消息单独 XACK |
| 提交 | `Commit()` 提交一个数字 | `XACK` 逐个确认 |
| 「批量提交」 | 天然支持（提交最后一条） | **没有这个概念** |

所以 redismq 必须**在「逐条 XACK」之上模拟出「区间提交」**。它的做法（`tools/mq/redismq/acker.go`）：

```go
// acker emulates Kafka's offset-commit semantics on top of per-entry XACK.
//
// Redis Streams ack individual entry ids, but the existing consumers commit only
// the LAST message of a batch and expect everything before it to be acked too
// (offset-range commit). We reproduce that with a monotonic watermark:
//
//   - onDeliver appends each delivered id, in stream order, to pending.
//   - mark raises the watermark to the highest processed id.
//   - flush XACKs every pending id <= watermark (a contiguous prefix, since both
//     pending and the watermark advance in id order) and drops them.
type acker struct {
    cli     redis.UniversalClient
    stream  string
    group   string

    mu        sync.Mutex
    pending   []string   // 已投递未确认的 id，按 id 升序
    watermark string     // 已 Mark 的最高 id；空表示还没 Mark 过
}

func (a *acker) onDeliver(id string) {
    a.mu.Lock()
    a.pending = append(a.pending, id)
    a.mu.Unlock()
}

func (a *acker) mark(id string) {
    a.mu.Lock()
    if compareID(id, a.watermark) > 0 {
        a.watermark = id
    }
    a.mu.Unlock()
}

func (a *acker) commit() {
    a.flush(context.Background())   // XACK 所有 pending <= watermark 的
}
```

**这段注释质量极高**，值得逐句分析：

**第一段**：说明问题。「Redis Stream 是逐条 XACK，但现有消费者只提交一批的最后一条，并且期望前面的也被确认（区间提交）。我们用单调水位来复现这个语义。」

**第二段**：三个动作。
- `onDeliver`：投递时按 stream 顺序追加到 `pending`
- `mark`：抬高水位到已处理的最高 id
- `flush`：XACK 所有 `pending` 里 ≤ watermark 的，并从 pending 移除

**第三段（最关键）**：契约说明。

```
CONTRACT (same one Kafka offset-commit relies on): marking id X must mean
every delivered id <= X is also done. This holds here because the stream is
single and ordered, one active consumer delivers it in id order, and the
batcher runs with syncWait=true — its OnComplete fires once per batch, AFTER
every worker for that batch has finished, and marks the batch's last id. So a
higher id is never marked while a lower delivered id is still in flight.
```

翻译 + 解读：
- **契约**：标记 X 就意味着 ≤ X 的都完成了（和 Kafka 一样）
- **为什么成立**：① stream 是单一且有序的；② 一个活跃消费者按 id 顺序投递；③ **batcher 的 `syncWait=true`**，所以 `OnComplete` 在每个 worker 都完成后才触发，标记的是批次最后一条
- **推论**：不会出现「高 id 被标记时，低 id 还在处理」的情况

**第四段**：一个刻意的设计选择。

```
We deliberately do NOT switch to a "longest contiguous explicitly-marked prefix"
model: that would be safer against an out-of-order mark, but it would also break
the OnlineHistory consumer, which marks ONLY the last id of a batch — under that
model nothing earlier would ever be acked. A future async (non-syncWait) consumer
that marks out of order would violate this contract; such consumers must mark
every processed id, not just the last.
```

翻译：**我们故意不采用「最长连续显式标记前缀」模型**。那个模型对乱序标记更安全，但它会破坏 OnlineHistory 消费者——因为它只标记批次的最后一条 id，在那种模型下前面的一条都不会被 ack。

这段话信息量很大：

1. **作者知道有更安全的方案**（严格的前缀匹配），但**主动放弃了**
2. 放弃理由是：**兼容现有消费者的行为**（只标记最后一条）
3. 并且明确警告：**未来如果有异步消费者乱序标记，必须标记每一条，不能只标最后一条**

> 🔴 **这段注释为什么值得学？**
>
> 因为它把一个**隐式的、跨模块的、容易被人改坏的契约**，用注释明确写下来了。这个契约涉及：
> - `batcher` 的 `syncWait=true`
> - `OnlineHistory` 消费者「只标记最后一条」的行为
> - redismq 的 watermark 算法
>
> 三者任何一个变了，都会导致**静默丢消息**。而代码本身不会报错——这是最危险的一类 bug。
>
> **写这种注释的能力，比写代码的能力更难，也更值钱。**

## 2.7 一次完整的 at-least-once 流程

把所有环节串起来，这是「消息为什么不会丢」的完整答案：

```
① 客户端 WS 发消息
   ↓
② rpc/msg.SendMsg：校验 + 补字段
   ↓
③ SendMessage(ctx, key=conversationID, MsgDataToMQ)
   · HashPartitioner → 同一会话固定 partition
   · SyncProducer 等 broker 确认（WaitForAll）
   · header 带 operationID
   ↓
④ Kafka toRedis（8 partition，磁盘持久化）  ← 消息在这里安全了
   ↓
⑤ msgtransfer 消费：Subscribe 取一条 → batcher 攒批
   · 攒 500 条 或 100ms
   · 按 key 哈希分发到 50 个 worker
   ↓
⑥ worker 处理：分配 seq → 写 Redis → 投 toMongo / toPush
   · 同一 key 的消息被聚合成一个 Msg，串行处理
   ↓
⑦ syncWait=true → counter.Wait() 等所有 worker 完成
   ↓
⑧ OnComplete：lastMessage.Mark() + Commit()
   ← 到这一步，位点才前进
   ↓
⑨ 如果 ⑤~⑦ 中途崩溃 → 位点没提交 → 重启后重投
   ↓
⑩ 重投的消息 → 幂等处理
   · 消息落库：doc_id 唯一索引，先 update 后 insert
   · 会话更新：$max 操作符
   · seq：号段预分配，重复只产生空洞
```

**这个流程能答「你们怎么保证消息不丢」**。核心就两句：
1. **位点在业务处理完成之后才提交**（`counter.Wait()` → `OnComplete` → `Mark/Commit`）
2. **重投的消息幂等**（唯一索引 + `$max` + 号段）

## 2.8 一个惊人的事实：本仓库的 kafka 封装没人用

`pkg/common/storage/kafka/` 有 394 行代码，7 个文件，实现了完整的 Producer / Consumer / TLS / health check。

**但业务链路完全不经过它**。

```bash
$ grep -rn "storage/kafka" --include="*.go" .
# 零结果

$ grep -rn "kafka\.NewKafkaProducer\b\|kafka\.NewMConsumerGroup\b\|kafka\.CheckHealth\|kafka\.CheckTopics" --include="*.go" .
./pkg/common/storage/kafka/...  （定义处）
./pkg/common/storage/controller/msg_transfer.go:47,51   （注释掉的代码）
./pkg/mqbuild/builder.go:160,172                        （调用的是 V2 版本）
./tools/check-component/main.go:60                       （唯一真实调用：CheckHealth）
```

**实际情况**：

| 代码 | 状态 |
|---|---|
| `Producer` + `SendMessage` | ❌ **零业务调用** |
| `MConsumerGroup` + `RegisterHandleAndConsumer` | ❌ **零业务调用** |
| `BuildProducerConfig` / `BuildConsumerGroupConfig` | 只被上面两个和 `CheckHealth` 用 |
| `CheckHealth` | ✅ `tools/check-component` 用它检查 Kafka 是否可用 |
| `CheckTopics` | ❌ 零调用 |

**为什么会有两份？**

看 `go.mod`：`github.com/openimsdk/tools` 是一个独立模块。演进路径大概是：

```
早期：OpenIM 自己写 kafka 封装（就是 pkg/common/storage/kafka）
   ↓
重构：抽到 tools 公共库，加了 V2 版本（实现 mq.Consumer 接口）
   ↓
结果：业务全迁到 V2，旧代码留在仓库里，只有 check-component 还在用
```

> ⚠️ **这不完全是死代码**。`check-component` 的 `CheckHealth` 确实用它（`tools/check-component/main.go:60`），而 `check-component` 是你每次启动前必跑的自检工具（`go run ./tools/check-component -c ./config`）。
>
> 所以严格说：**`verify.go` 是活的，其余 330 行是死的**。

> 🔴 **这个发现的价值**：它说明「代码存在」不等于「代码在跑」。面试如果被问「你们的 Kafka 生产者也用了 sarama 吗」，你可以答「我们是两套，业务走 tools 库的封装，仓库里还留了一套早期的实现，只有自检工具在用」——**这种细节一听就是真读过代码**。

## 2.9 可靠性缺口清单

把本篇发现的问题汇总（11 篇已有部分，这里从 MQ 角度重新表述）：

| # | 位置 | 问题 | 后果 |
|---|---|---|---|
| 1 | `internal/msgtransfer/init.go:128-131` | `fn` 永远返回 nil，`HandleChatWs2Mongo` 的错误被吞掉 | **落库失败仍提交位点 → 消息永久丢失** |
| 2 | `online_msg_to_mongo_handler.go:28` | `HandleChatWs2Mongo` 无返回值 | 同上，是同一个问题的两个面 |
| 3 | `pkg/tools/batcher/batcher.go:272` | `Do` 无返回值 | worker 内的业务失败无法上报，位点照常前进 |
| 4 | `pkg/tools/batcher/batcher.go:262-278` | worker 没有 `defer recover()` | 一条畸形消息 panic 崩整个进程（外部 kafka 消费者的 `ConsumeClaim` 有 recover，这里没有） |
| 5 | `internal/msgtransfer/init.go:117-125` | `Subscribe` 返回 error 时 `cancel` + `return` | 单条消息处理失败 → 整个 msgtransfer 退出，不重连 |
| 6 | `internal/push/push.go:103-108` | 同上，两个消费者都是 | 推送服务同理 |
| 7 | `internal/msgtransfer/init.go:118` | `for { Subscribe }` 是**必需的**（Subscribe 每次只处理一条），不是死代码 | 📌 **纠正之前会话的判断**：这个 for 循环不是 bug，它就是「逐条消费」的主循环 |
| 8 | `internal/push/push_handler.go:276` | 群消息的 key 用 `SendID_RecvID`，RecvID 为空 | 所有群消息挤同一 partition |
| 9 | `pkg/mqbuild/builder.go:172` | `autoCommitEnable=true` 同时手动 `Commit()` | 语义重叠，可能提前提交 |

**第 7 条要特别说明**：我之前的笔记里写「`for {}` 是死代码」是**错的**。因为 `mq.Consumer.Subscribe` 的语义是「取一条消息处理完就返回」，所以必须用循环不断取。真正的 bug 是**循环体里 `return` 了**——出错就退出，不重连。

```go
// 现状（有问题）
for {
    if err := consumer.Subscribe(ctx, fn); err != nil {
        cancel(err)
        return          // ← 出错就永久退出
    }
}

// 正确写法
for {
    if err := consumer.Subscribe(ctx, fn); err != nil {
        if ctx.Err() != nil { return }     // ctx 已取消，正常退出
        log.ZWarn(ctx, "subscribe err", err)
        time.Sleep(time.Second)            // 重连退避
        continue
    }
}
```

> ⚠️ **对比 `pkg/common/storage/kafka/consumer_group.go:37-50` 的 `RegisterHandleAndConsumer`**：它是对的——区分了 `ErrClosedConsumerGroup` / `context.Canceled`（退出）和其他错误（继续循环）。**但这段对代码没人用**。所以「正确的实现存在，但业务用错了」——这比「完全没写对」更讽刺，也更值得在面试里讲。

## 2.10 消息积压的实战排查

如果你在自己的环境里把 msgtransfer 停掉，然后发 1000 条消息，会看到什么？

```bash
# 1. 看消费组的积压（每个 partition 的 lag）
docker exec -it kafka kafka-consumer-groups.sh \
  --bootstrap-server localhost:9092 \
  --describe --group redis

# 输出类似：
# GROUP  TOPIC    PARTITION  CURRENT-OFFSET  LOG-END-OFFSET  LAG
# redis  toRedis  0          100             1100            1000   ← 积压 1000
# redis  toRedis  1          50              60              10
```

**`LAG = LOG-END-OFFSET - CURRENT-OFFSET`** 就是积压量。

```bash
# 2. 重启 msgtransfer，观察 LAG 是否下降
mage start

# 3. 再看
docker exec -it kafka kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group redis
```

**这个实验能直观看到 MQ 的削峰作用**：生产 1000 条是瞬间的，消费是匀速的，中间的差额就是 LAG。

---

# 第三部分　实验

### 🧪 实验 23-1：看 topic 的真实结构

```bash
# 列出所有 topic
docker exec -it kafka kafka-topics.sh --bootstrap-server localhost:9092 --list

# 看 toRedis 的分区数和副本数
docker exec -it kafka kafka-topics.sh --bootstrap-server localhost:9092 \
  --describe --topic toRedis

# 预期输出：
# Topic: toRedis  PartitionCount: 8  ReplicationFactor: 1
# （单节点 KRaft，副本数只能是 1）
```

**重点看 `PartitionCount: 8`**——这就是消费并行度的天花板。

### 🧪 实验 23-2：制造积压并观察恢复

```bash
# 1. 停掉 msgtransfer（只停它，别全停）
#    最简方式：mage stop 然后手动只起其他服务（麻烦）
#    或者直接发一批消息，趁 msgtransfer 处理前看 LAG

# 2. 更方便的做法：直接往 topic 灌测试消息
docker exec -it kafka bash -c "
  seq 1 1000 | kafka-console-producer.sh \
    --bootstrap-server localhost:9092 \
    --topic toRedis \
    --property parse.key=true \
    --property key.separator=:
" <<< "$(for i in $(seq 1 1000); do echo "testkey_$((i%5)):garbage_$i"; done)"

# 3. 看 LAG（会暴涨）
docker exec -it kafka kafka-consumer-groups.sh \
  --bootstrap-server localhost:9092 --describe --group redis

# 4. 观察 msgtransfer 的日志（会疯狂报 unmarshal 失败）
#    这就是「毒消息」——protobuf 解析不了的垃圾数据
```

**这个实验会让你看到一个真实的运维问题**：毒消息会让消费者无限报错。OpenIM 没有死信队列，所以只能刷日志。

### 🧪 实验 23-3：验证 key 决定 partition

```bash
# 同一个 key 连发 10 条，看是否落在同一分区
for i in $(seq 1 10); do
  echo "conv_test:msg_$i"
done | docker exec -i kafka kafka-console-producer.sh \
  --bootstrap-server localhost:9092 --topic toRedis \
  --property parse.key=true --property key.separator=:

# 用 kafka-console-consumer 带 partition 信息读出来
docker exec -it kafka kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic toRedis \
  --from-beginning --property print.partition=true --property print.key=true \
  --timeout-ms 3000

# 预期：conv_test 的消息全部在同一个 partition
```

---

# 第四部分　自测题

1. **MQ 的三大作用里，OpenIM 真正依赖的是哪个？** 用具体数字说明。
2. **Kafka 只保证 partition 内有序，为什么对 IM 够用？** 怎么把一个会话的消息固定到同一 partition？
3. **partition 数为什么限制了消费端的扩容上限？** OpenIM 配了几个？
4. **at-least-once 和 at-most-once 的区别，就在一行代码**——是哪一行？
5. **为什么「标记一批的最后一条」等价于「标记全部」？** 这个等价依赖什么前提？如果破坏会怎样？
6. **redismq 为什么不用「最长连续标记前缀」模型？** 它在注释里怎么解释的？
7. **Kafka 的三种 ack 级别分别是什么？** OpenIM 默认用哪个？
8. **消费积压时，为什么不能直接扩 partition？** 会破坏什么？
9. **内存队列（simmq）为什么只能 standalone 用？**
10. **本仓库的 `pkg/common/storage/kafka` 有多少是死代码？** 哪一部分还活着？

---

# 第五部分　面试话术

**问：为什么用 Kafka，不用 RabbitMQ / RocketMQ？**

> 「主要是两个原因。
> 第一是**吞吐量**。Kafka 的顺序写磁盘 + 零拷贝，单机能到几十万条/秒。我们的峰值是几万条消息/秒，还要乘以扇出（一条群消息要推给几百人），RabbitMQ 在这个量级下容易堆积。
> 第二是**分区内有序**。Kafka 用 key 哈希分区，同一个会话的消息保证落在同一 partition，消费端天然有序。RabbitMQ 的队列没有这个语义，要自己做单队列消费，扩展性差。
> 另外我们的 MQ 层做了抽象，`queue: kafka / redis / memory` 可以切。小规模部署可以只起 Redis Stream，不用维护 Kafka。」

**追问：怎么保证消息不丢？**

> 「位点在业务处理完成之后才提交。
> 具体链路是：msgtransfer 消费 toRedis，用 batcher 攒 500 条或 100ms，然后按 key 哈希分发到 worker。这里有个关键配置 `syncWait=true`——分发之后调 `counter.Wait()`，等所有 worker 处理完，才触发 `OnComplete` 去 `Mark + Commit`。
> 所以如果中途崩溃，位点没前进，重启后会重投。
> 重投的消息要幂等：落库用 `doc_id` 唯一索引 + 先 update 后 insert；会话的 max_seq 用 `$max` 操作符；seq 是号段预分配，重复只产生空洞不会重复。
> 生产端用的是 `WaitForAll`，等所有 ISR 确认才算发送成功。」

**追问：那有没有可能丢？**

> 「有，我找到几个真实的缺口。
> 最严重的是 `HandleChatWs2Mongo` 没有返回值，失败时只记日志不返回 error，外层 `fn` 永远返回 nil。结果就是——消息 1 落库失败不 Mark，消息 2 成功 Mark 然后 Commit，位点直接跳过消息 1，这条消息永久丢失。
> 修法是把返回值改成 error，让 `Subscribe` 返回错误从而不提交位点。但这样又要有死信队列或重试上限，否则毒消息会卡死 partition。我们现在两者都没有。
> 另外群聊的离线推送用了 `SendID_RecvID` 做 key，群消息 RecvID 是空的，所有群挤在同一个 partition，高峰期会严重积压。」

**追问：at-least-once 和 exactly-once 你们用哪个？**

> 「at-least-once 加幂等消费，效果等价于 exactly-once。
> 严格的 exactly-once 只在 Kafka 内部的流处理（Kafka Streams）才真正成立。我们是 Kafka → MongoDB，跨系统，不存在真正的事务。所以正确的说法是『至少一次投递 + 幂等处理』。
> 面试里如果有人说『我们保证 exactly-once』，要么是在说 Kafka 内部的场景，要么是没搞清楚。」

---

> **下一篇**：24 本地缓存与三级缓存。会讲清 LRU 为什么用「哈希表 + 双向链表」、500 个分片解决什么、以及 OpenIM 最精巧的一个设计——**缓存依赖图 + 级联失效**（删一个好友列表，自动删掉所有「是否好友」的判定缓存）。
