# 21 MongoDB 数据模型全集

> **本篇定位**：Part5 的第一篇。Part1~4 讲的是「业务怎么跑」，Part5 讲的是「数据落在哪、为什么这么落」。
> **前置**：13（消息存储模型，已讲过 msg 集合的分桶设计，本篇不重复，只做整体定位）、16-20（业务域，知道有哪些集合）。
> **阅读方式**：第一部分纯理论（MongoDB 是什么、索引原理、为什么不用事务）；第二部分才是本仓库 18 个集合逐个拆解。
> **本篇标记**：🔴 重点　🟡 难点　⚠️ 易错　🧪 实验　📌 待验证

---

# 第一部分　理论：MongoDB 到底解决了什么问题

## 1.1 从「一张表」开始

你在 bluebell 里写过这样的表：

```sql
create table article (
  id bigint primary key auto_increment,
  title varchar(200),
  content text,
  author_id bigint,
  create_time datetime,
  index idx_author (author_id)
);
```

这套东西你已经很熟了。现在要做一个 IM，第一反应肯定是照搬：

```sql
create table user (
  user_id varchar(64) primary key,
  nickname varchar(64),
  face_url varchar(500),
  ex varchar(1024),
  create_time datetime
);

create table friend (
  owner_user_id varchar(64),
  friend_user_id varchar(64),
  remark varchar(64),
  primary key (owner_user_id, friend_user_id)
);
```

**这些表完全能跑，而且 MySQL 做这些比 MongoDB 更好**——强 schema、强事务、成熟的运维体系。

所以问题不是「MongoDB 比 MySQL 强」，而是**「消息和会话这两个实体，关系模型撑不住」**。

13 篇已经论证过消息为什么不能用关系表（写放大 500 倍、行数无上限、payload 变长）。这里补上会话：

```sql
-- 会话如果用关系表
create table conversation (
  owner_user_id varchar(64),
  conversation_id varchar(64),
  max_seq bigint,      -- 不断被 UPDATE
  unread_count int,
  is_pinned bool,
  recv_msg_opt int,
  ...
);
```

会话的关键特征是**高频 UPDATE 同一行**：每来一条消息，这个会话的 `max_seq` 就要更新一次。500 人的活跃群，一条消息 = 500 次 UPDATE（每人一份会话）。这就是**行锁热点**，MySQL 在这个场景下会疯狂排队。

> 🔴 **一句话结论**：IM 的核心数据不是「读多写少的静态实体」，而是「**高频更新同一行的状态**」+「**无限追加的日志**」。这两类数据的访问模式和关系库的设计初衷冲突。

## 1.2 文档数据库的三个关键差异

名字叫「文档数据库」，但**这里的文档不是 Word 文档，是 JSON 文档**。一条记录长这样：

```json
{
  "_id": ObjectId("65f1a2b3c4d5e6f7a8b9c0d1"),
  "owner_user_id": "user1",
  "conversation_id": "si_user1_user2",
  "max_seq": 1024,
  "is_pinned": false,
  "recv_msg_opt": 0
}
```

对比关系库，有三个本质差异：

### 差异一：没有固定 schema（弱 schema）

同一张表（MongoDB 叫「集合」collection）里，两条记录可以有完全不同的字段：

```json
{ "user_id": "u1", "nickname": "a" }
{ "user_id": "u2", "nickname": "b", "extra_field": 123 }   // 加字段不需要 DDL
```

**好处**：改字段不用跑 `ALTER TABLE`（大表上是灾难），灰度发布时新旧字段可以共存。
**代价**：没有数据库层面的约束。字段拼错、类型错误、必填项缺失，全靠应用层自己保证。

> ⚠️ **易错点**：很多人以为「无 schema = 随便写」。实际上是「**schema 搬到了应用层**」——OpenIM 用 Go 的 struct + `bson` tag 来强制约束。你写错 tag，数据库不会报错，但查不出来数据，这种 bug 极难定位。

### 差异二：单文档原子性（这是设计的分水岭）

MongoDB 保证**单个文档**的读写在并发下是原子的，不保证跨文档。

这一条决定了整个数据模型的设计走向：

```
需要原子性的东西 → 塞进同一个文档
不需要原子性的东西 → 拆成不同文档
```

举个 OpenIM 的实际例子：消息和它的「已读状态」。

```
方案 A：一条消息一个文档，已读状态存在另一个集合
        → 标记已读 = 改另一个集合，跨文档，不原子
方案 B：一条消息一个文档，已读状态塞进去
        → 但一条消息有 500 个用户，一人一份已读状态 → 文档爆炸
方案 C（OpenIM 选的）：一条消息一个文档，只存「谁删了」（del_list 数组）
        → 已读状态拆出去，用「水位」表达（14 篇讲过）
```

**看明白了没**：因为不能跨文档事务，所以「已读」不能设计成「每人一个标记」，只能设计成「每人一个水位 maxSeq」。这不是巧合，是**被存储引擎的能力逼出来的业务设计**。

### 差异三：嵌套数组（一把双刃剑）

文档里可以放数组：

```json
{
  "doc_id": "si_a_b:0",
  "msg": [
    { "seq": 1, "content": "hi" },
    { "seq": 2, "content": "hello" }
  ]
}
```

**好处**：一次查询拿回 100 条消息，一次写入 100 条消息，省了 99 次网络往返。
**代价**：数组是有上限的（单文档 16MB），而且数组越大，读写越慢（每次都要把整个文档读进内存再改）。

这就是 13 篇讲的「100 条一桶」的由来：**桶太大 → 单文档膨胀；桶太小 → 文档数量爆炸、索引变大**。100 是这个平衡点。

## 1.3 索引：MongoDB 性能和 MySQL 的根本区别

### B 树、B+ 树，一句话讲清

先建立常识：

- **B 树**：每个节点既存 key 也存数据。查到了就返回，不用走到叶子。
- **B+ 树**：只有叶子节点存数据，非叶子节点只存 key；叶子节点之间用链表串起来。

MySQL InnoDB 用 B+ 树，原因是**范围查询**：`where id between 1 and 100` 找到起点后顺着叶子链表扫一遍就完了，不用回溯到上层节点。另外非叶子节点不存数据，同样大小的页能存更多 key，树更矮（3 层能存几千万行）。

MongoDB 的索引是 **B 树**（准确地说是 B-tree 的变种），因为文档数据库的主要访问模式是「**按 _id 精确查一个文档**」，点查优先。

> 这个差异不用背，你只要记住：**两者都是「多路平衡树 + 磁盘页」**。理解到「一层索引 = 一次磁盘 IO」这个层面，就足够推导所有优化问题了。

### 复合索引与最左前缀（面试高频）

OpenIM 里有一堆复合索引，比如会话集合：

```go
mongo.IndexModel{
    Keys: bson.D{
        {Key: "owner_user_id", Value: 1},
        {Key: "conversation_id", Value: 1},
    },
    Options: options.Index().SetUnique(true),
}
```

`bson.D` 是**有序**的 map（`bson.M` 是无序的）。顺序很重要，因为它决定了索引的排序方式：先按 `owner_user_id` 排，相同再按 `conversation_id` 排。

于是：

| 查询条件 | 能否用上这个索引 |
|---|---|
| `owner_user_id = X` | ✅ 能用（最左前缀） |
| `owner_user_id = X AND conversation_id = Y` | ✅ 能用（完整） |
| `conversation_id = Y`（不带 owner） | ❌ 用不上（跳过了最左列） |

> 🔴 **面试点**：为什么「最左前缀」？因为索引是先按第一列排序、第一列相同才按第二列排序的。跳过第一列，整个索引在第二列上是**无序**的，没法二分查找。

这也是为什么 OpenIM 在 `conversation` 集合上建了**两个**索引：`(owner_user_id, conversation_id)` 唯一索引 + `user_id` 单列索引。因为有两种查询模式：
- 「我的会话列表」→ 按 owner 查
- 「这个用户参与的所有会话」→ 按 user_id 查（群解散时要清所有人会话）

### 唯一索引的真正作用：不是查询，是防并发

OpenIM 里大量用 `SetUnique(true)`。很多人以为唯一索引只是为了「不重复」，实际上它在 IM 里承担了**幂等**的职责：

```
两个并发请求同时「加好友」：
  没有唯一索引 → 插入两条 (user1, user2) 记录 → 脏数据
  有唯一索引   → 第二条插入失败（E11000）→ 应用层捕获并忽略 → 幂等
```

> ⚠️ **一个真 bug**：`seq` 集合的 `conversation_id` 索引**没有**设 `Unique: true`（`mgo/seq_conversation.go:16-20`）。并发首次 upsert 时可能插入两条文档，`$inc` 分别作用在不同文档上，导致 **seq 重复**。这个 bug 在 12 篇提过，21 篇从索引角度看，根因就是「忘了设唯一」。

### 索引的代价

索引不是白建的：

```
写入一条文档 → 要更新所有相关索引 → 每个索引一次 B 树插入
```

OpenIM 的 `msg` 集合只建了**一个**索引（`doc_id` 唯一），而且是**写多读少**的集合。这是刻意的取舍：**消息写入路径上，索引越少越快**。

反过来看 `s3` 集合（对象存储元数据），建了三个索引（`name` 唯一、`create_time`、`key`），因为它是读多写少。

> 🟡 **难点**：怎么判断该不该建索引？看**读写比**。写多 → 少建；读多 → 多建。这是一条能直接用在面试里的经验法则。

## 1.4 为什么 OpenIM 几乎不用事务

MongoDB 4.0 开始支持多文档事务（副本集），4.2 支持分片集群事务。但 OpenIM 里只有 **5 处**用到事务：

```go
// pkg/common/storage/controller/client_config.go:35
// pkg/common/storage/controller/conversation.go:84, :198, :246
// pkg/common/storage/controller/group.go（群解散）
```

其余所有写操作都是**单文档原子写**或**多次独立写 + 幂等**。

为什么？三个理由：

**第一，性能。** 事务要开 session、要走两阶段提交、要等多数派确认。在消息落库这种每秒几万条的场景里，这是不可接受的开销。

**第二，跨服务的写操作没法用事务。** 一条消息落库时，要同时写 Redis（seq）、写 Mongo（消息）、发 Kafka（推送）。这三个是**不同系统**，MongoDB 事务管不了。分布式事务（2PC/TCC/Saga）的复杂度远超收益。

**第三，最终一致性已经够用。** 消息丢了可以重投（Kafka 位点不提交），重复了可以幂等（`doc_id` 唯一索引），乱序了可以靠 seq 排序。**只要每个环节都幂等，就不需要分布式事务**。

> 🔴 **一句话记住**：**幂等 > 事务**。能用「唯一索引 + 覆盖写」解决的，绝不上事务。

### 但事务真的被用到了，而且有个很聪明的降级

`mongoutil` 的实现值得细看（外部模块 `github.com/openimsdk/tools/db/mongoutil/tx.go`）：

```go
func (m *mongoTx) init(ctx context.Context) error {
    var res map[string]any
    if err := m.client.Database("admin").RunCommand(ctx, bson.M{"isMaster": 1}).Decode(&res); err != nil {
        return errs.WrapMsg(err, "check whether mongo is deployed in a cluster")
    }
    if _, allowTx := res["setName"]; !allowTx {
        return nil // non-clustered transactions are not supported
    }
    m.tx = func(fnctx context.Context, fn func(ctx context.Context) error) error {
        sess, err := m.client.StartSession()
        ...
        _, err = sess.WithTransaction(fnctx, ...)
        return errs.WrapMsg(err, "mongodb transaction failed")
    }
    return nil
}

func (m *mongoTx) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
    if m.tx == nil {
        return fn(ctx)   // ← 关键：没有事务能力时，直接执行
    }
    return m.tx(ctx, fn)
}
```

这段代码做了一件事：**启动时探测 MongoDB 是不是副本集**。

- 探测方式：跑 `isMaster` 命令，看返回里有没有 `setName` 字段。有 = 副本集（支持事务），没有 = 单机（不支持）
- 有事务：`Transaction` 走 `StartSession` + `WithTransaction`
- 没有事务：`Transaction` **直接调用 fn**，退化成「顺序执行多个写操作」

> 🔴 **这是本篇最值得学的一个设计**：同一份业务代码，在单机（开发环境）和副本集（生产环境）都能跑。单机下语义减弱（可能部分成功），副本集下语义完整。**用「能力探测 + 降级」代替「环境分支」**——代码里没有任何 `if isDev` 的痕迹。

这正好解释了你本地 `config/mongodb.yml` 里 `mongoMode: "standalone"` 时为什么事务不生效：`im_v3` 跑的是单机 Mongo，`setName` 不存在，`m.tx` 为 `nil`。

> 🧪 **实验 21-1**：验证事务降级
> ```bash
> # 跑一下 isMaster，看重置有没有 setName
> docker exec -it mongo mongosh -u im_app -p <见凭据> --authenticationDatabase im_v3 --eval 'db.adminCommand({isMaster:1})'
> ```
> 单机模式下输出里**没有** `setName` 字段。这就是事务降级的开关。

## 1.5 副本集 vs 分片集群：OpenIM 为什么只选前者

MongoDB 有两种部署模式：

| 模式 | 解决什么问题 | 代价 |
|---|---|---|
| **副本集（Replica Set）** | 高可用：主节点挂了自动切从 | 存不下更多数据（每个节点都是全量） |
| **分片集群（Sharded Cluster）** | 水平扩展：数据分散到多个分片 | 复杂：要配 shard key、mongos 路由、跨分片查询慢 |

OpenIM 的 `config/mongodb.yml` 里只有 `replicaSet` 配置，**没有任何分片配置**：

```yaml
mongoMode: "standalone"   # 可选 standalone / replicaSet
replicaSet:
  name: rs0
  hosts: [127.0.0.1:37017, 127.0.0.1:37018, 127.0.0.1:37019]
  readConcern: majority
readPreference:
  mode: primary
```

为什么不做分片？因为 **OpenIM 已经在应用层做了分片**：

```
消息分桶：一条会话的消息按 seq 切成 N 个文档（doc_id = "si_a_b:0", "si_a_b:1", ...）
         → 单文档不超过 16MB，且写入分散在不同文档上
会话分片：会话按 owner_user_id 天然分布在不同的文档上
         → 没有单文档热点
```

**应用层分片比数据库分片更灵活**：分片键可以按业务改，不需要 DBA 介入，也不会有跨分片查询的问题（因为每次查询都带着完整的分片键）。

> 🟡 **要注意的取舍**：应用层分片的代价是**失去了全局有序扫描的能力**。你想「查全库最近 100 条消息」，在 OpenIM 里做不到——必须指定会话。这不是缺陷，是 IM 的业务特点：**没人需要跨会话的全局消息流**。

`readPreference.mode: primary` 也值得说一句：从库读虽然能分担压力，但会有**复制延迟**——刚发的消息立刻读不到。消息场景不能容忍这个，所以强制读主库。这是「一致性优先于吞吐」的明确选择。

---

# 第二部分　源码：18 个集合逐个拆解

## 2.1 集合总表

`pkg/common/storage/database/name.go` 定义了所有集合名：

```go
const (
    BlackName               = "black"                  // 黑名单
    ConversationName        = "conversation"           // 会话（用户视角）
    FriendName              = "friend"                 // 好友关系（双向两份）
    FriendVersionName       = "friend_version"         // 好友版本日志
    FriendRequestName       = "friend_request"         // 好友申请
    GroupName               = "group"                  // 群信息
    GroupMemberName         = "group_member"           // 群成员（M:N）
    GroupMemberVersionName  = "group_member_version"   // 群成员版本日志
    GroupJoinVersionName    = "group_join_version"     // 入群版本日志
    ConversationVersionName = "conversation_version"   // 会话版本日志
    GroupRequestName        = "group_request"          // 入群申请
    LogName                 = "log"                    // 客户端日志上报
    ObjectName              = "s3"                     // 对象存储元数据（注意集合名是 s3）
    UserName                = "user"                   // 用户
    SeqConversationName     = "seq"                    // 会话 seq 分配器（注意集合名是 seq）
    SeqUserName             = "seq_user"               // 用户级 seq（min/max/read）
    StreamMsgName           = "stream_msg"             // 流式消息（未深入，见 2.9）
    CacheName               = "cache"                  // 缓存（图片缩略图等）
)
```

三个**命名不一致**的坑，先说清楚避免你去找 `object` 集合：

| 业务名 | 常量名 | 实际集合名 |
|---|---|---|
| 对象存储 | `ObjectName` | `s3` |
| 会话 seq | `SeqConversationName` | `seq` |
| 消息 | （无常量） | `msg` |

`msg` 集合甚至没有在 `name.go` 里定义常量，它的集合名硬编码在别处。

> ⚠️ **踩坑提醒**：这些是历史命名遗留。查数据库时 `show collections` 你会看到 `s3`、`seq`、`msg`，不要以为有单独的 `object` 集合。

## 2.2 用户集合 `user`

```go
// pkg/common/storage/model/user.go
type User struct {
    UserID           string    `bson:"user_id"`
    Nickname         string    `bson:"nickname"`
    FaceURL          string    `bson:"face_url"`
    Ex               string    `bson:"ex"`
    AppMangerLevel   int32     `bson:"app_manger_level"`    // 注意拼写：Manger 不是 Manager
    GlobalRecvMsgOpt int32     `bson:"global_recv_msg_opt"`
    CreateTime       time.Time `bson:"create_time"`
}
```

索引：

```go
// pkg/common/storage/database/mgo/user.go:22
mongo.IndexModel{
    Keys:    bson.D{{Key: "user_id", Value: 1}},
    Options: options.Index().SetUnique(true),
}
```

**字段设计点评**：

- **只有 7 个字段**。对比微信的 user 表（几十个字段），OpenIM 把用户信息压到了最小。为什么？因为 `user` 集合的增长速度和**用户数**成正比，不是和消息量成正比。字段越多，索引越大，缓存越难命中。所有扩展信息都塞进 `Ex`（客户端自定义的 JSON 字符串）。
- **`AppMangerLevel` 是权限等级**，不是管理员的元数据。取值 `0` = 普通用户，`>0` = 管理员（数字越大权限越高，见 16 篇）。这里有个**拼写错误**：`Manger` 应为 `Manager`，全项目都这么写，属于历史包袱。
- **`GlobalRecvMsgOpt` 是「全局免打扰」**：`0` 正常、`1` 接收但不通知、`2` 不接收。它和「会话级 `recv_msg_opt`」是**两层**：全局是兜底，会话级覆盖全局。
- 🔴 **没有密码字段**。OpenIM 不管密码——认证由业务后端签发 JWT（16 篇讲过），OpenIM 只验 token。这个设计让 OpenIM 可以嵌进任何账号体系，是它作为「IM 中间件」而非「完整产品」的体现。

## 2.3 好友集合 `friend`

```go
// pkg/common/storage/model/friend.go
type Friend struct {
    ID             primitive.ObjectID `bson:"_id"`
    OwnerUserID    string             `bson:"owner_user_id"`
    FriendUserID   string             `bson:"friend_user_id"`
    Remark         string             `bson:"remark"`
    CreateTime     time.Time          `bson:"create_time"`
    AddSource      int32              `bson:"add_source"`
    OperatorUserID string             `bson:"operator_user_id"`
    Ex             string             `bson:"ex"`
    IsPinned       bool               `bson:"is_pinned"`
}
```

索引：

```go
// mgo/friend.go:26 — (owner_user_id, friend_user_id) 唯一
```

**关键设计：一条好友关系存两份。**

```
user1 加 user2 为好友 → 插入两条文档：
  { owner: user1, friend: user2, remark: "老王", is_pinned: true  }
  { owner: user2, friend: user1, remark: "",     is_pinned: false }
```

为什么不存一份（`user1 < user2`）？因为**方向性字段**：`remark`（备注）和 `is_pinned`（置顶）是「我对他的设置」，不是「我们俩的设置」。如果只存一份，就得用 `map[userID]remark` 这种结构，查询时要按当前用户取值——索引和查询都变复杂。

代价是：**删除好友必须删两条**，而且两条要一致（17 篇讲过这是唯一没有事务保护的地方，靠最终一致）。

> 🔴 **对比 `ObjectID` 的使用**：`friend` 用了 `primitive.ObjectID` 作 `_id`，而 `user` / `group` 没有显式 `_id`（让 MongoDB 自动生成）。为什么？因为 `friend` 没有天然的业务主键（`(owner, friend)` 组合），而 `user` / `group` 有 `user_id` / `group_id` 可以直接当查询键。**有没有 `_id` 不重要，重要的是有没有可用的唯一索引**——`friend` 的 `(owner, friend)` 唯一索引承担了主键的职责。

## 2.4 好友申请 `friend_request` 与黑名单 `black`

```go
// model/friend_request.go（字段略，核心是状态机）
// handle_result: -1 已拒绝 / 0 未处理 / 1 已同意
```

索引（`mgo/friend_request.go:21`，`CreateMany`）：

```go
{{Key: "from_user_id", Value: 1}, {Key: "to_user_id", Value: 1}}   // 查「我发给谁的」
{{Key: "to_user_id", Value: 1}}                                     // 查「谁发给我的」
```

两条索引对应两种页面：**「我发出的申请」**和**「我收到的申请」**。

黑名单：

```go
// model/black.go
type Black struct {
    OwnerUserID    string
    BlockUserID    string
    CreateTime     time.Time
    Ex             string
}
```

索引：`(owner_user_id, block_user_id)` 唯一。

17 篇讲过：**拉黑不删好友，只做单向阻断**。所以 `black` 是独立集合，不是 `friend` 的一个字段。

## 2.5 群与群成员

```go
// model/group.go
type Group struct {
    GroupID                string
    GroupName              string
    Notification           string      // 群公告
    Introduction           string
    FaceURL                string
    CreateTime             time.Time
    Ex                     string
    Status                 int32       // 0 正常 / 1 被封 / 2 已解散 / 3 禁言
    CreatorUserID          string
    GroupType              int32
    NeedVerification       int32       // 入群验证
    LookMemberInfo         int32       // 谁能看成员
    ApplyMemberFriend      int32       // 能否加群友
    NotificationUpdateTime time.Time
    NotificationUserID     string
}

// model/group_member.go
type GroupMember struct {
    GroupID        string
    UserID         string
    Nickname       string    // 群内昵称（可覆盖用户昵称）
    FaceURL        string
    RoleLevel      int32     // 20 群主 / 60 管理员 / 100 普通
    JoinTime       time.Time
    JoinSource     int32
    InviterUserID  string
    OperatorUserID string
    MuteEndTime    time.Time // 成员禁言到什么时候
    Ex             string
}
```

索引：

```go
// group.go:20 — group_id 唯一
// group_member.go:21 — (group_id, user_id) 唯一
```

**为什么群成员是独立集合而不是群文档里的数组？**

18 篇详细讲过，这里从存储角度补三条：

1. **16MB 上限**：一个 10 万人的群，成员数组轻松超过限制
2. **无法反查**：「这个用户加入了哪些群」需要遍历所有群的 members 数组（全表扫）
3. **并发冲突**：两个人同时入群，都要改群文档的 members 数组 → 文档级锁串行化

M:N 中间表（`group_member`）解决全部三个问题，代价是「查群成员」要 join（MongoDB 没有 join，要两次查询或用 `$lookup`）。

> 🟡 **`RoleLevel` 的取值反直觉**：`20` 是群主，`60` 是管理员，`100` 是普通成员。**数字越小权限越大**。这种设计在权限判断时可以写 `roleLevel <= 60` 表示「管理员及以上」，比 `roleLevel >= 2` 更不容易出错（加新角色时不用改所有判断）。

> 💡 **Nickname 的必要性**：群成员有自己的 `nickname` 和 `face_url`，可以覆盖用户全局的。这是 IM 的常见需求（在老板群里叫「张总」，在朋友群里叫「老张」）。存储上的代价是**冗余**：用户改了昵称，所有群的成员记录都要更新——OpenIM 没有做这个同步，所以群内昵称改了就固定了。📌 待验证：是否有同步机制（我在 `internal/rpc/group/group.go` 里没找到「用户改昵称 → 更新群成员」的调用）。

## 2.6 会话集合 `conversation`

```go
// model/conversation.go
type Conversation struct {
    OwnerUserID           string    // 会话的「主人」
    ConversationID        string
    ConversationType      int32     // 1 单聊 / 2 群聊 / 3 通知
    UserID                string    // 单聊对方的 userID
    GroupID               string    // 群聊的 groupID
    RecvMsgOpt            int32     // 0 正常 / 1 免打扰 / 2 免打扰+不接收
    IsPinned              bool
    IsPrivateChat         bool      // 阅后即焚会话
    BurnDuration          int32     // 焚毁时长（秒）
    GroupAtType           int32     // @状态：0 无 / 1 @我 / 2 @所有人 / 3 @我和所有人
    AttachedInfo          string
    Ex                    string
    MaxSeq                int64     // 会话内最大 seq
    MinSeq                int64     // 会话内最小 seq（清理/焚毁后的水位）
    CreateTime            time.Time
    IsMsgDestruct         bool
    MsgDestructTime       int64
    LatestMsgDestructTime time.Time
}
```

索引（`mgo/conversation.go:22`，`CreateMany`）：

```go
{ {owner_user_id:1}, {conversation_id:1} }  SetUnique(true)   // 主查询路径
{ {user_id:1} }                                                // 反查：用户参与的所有会话
```

**18 个字段的设计逻辑**（19 篇已逐条讲过，这里只做归类）：

| 类别 | 字段 | 为什么在会话上 |
|---|---|---|
| **身份** | owner_user_id, conversation_id | 定位 |
| **类型** | conversation_type, user_id, group_id | 区分单聊/群聊/通知 |
| **用户偏好** | recv_msg_opt, is_pinned, group_at_type | 每人独立，无法放在群/用户上 |
| **生命周期** | max_seq, min_seq | 同步的边界（14 篇） |
| **阅后即焚** | is_private_chat, burn_duration, is_msg_destruct, msg_destruct_time, latest_msg_destruct_time | 5 个字段做一件事，见下 |
| **扩展** | attached_info, ex | 客户端自定义 |

> 🟡 **为什么要 5 个字段做「阅后即焚」？** 因为焚毁是**周期性**的：`burn_duration` 是「消息存活多久」，`msg_destruct_time` 是「下次焚毁检查的时间点」，`latest_msg_destruct_time` 是「上次焚毁时间」，`is_msg_destruct` 是「是否已开启」，`is_private_chat` 是「是否私密会话」。这套设计让定时任务能**按时间索引扫描**「该焚毁的会话」，而不是全表遍历。

## 2.7 seq 与 seq_user：两个分配器

```go
// model/seq.go — 会话级
type SeqConversation struct {
    ConversationID string
    MaxSeq         int64
    MinSeq         int64
}

// model/seq_user.go — 用户级
type SeqUser struct {
    UserID         string
    ConversationID string
    MinSeq         int64
    MaxSeq         int64
    ReadSeq        int64
}
```

索引：

```go
// seq_conversation.go:16 — conversation_id（⚠️ 没有 Unique）
// seq_user.go:16 — (user_id, conversation_id)（⚠️ 也没有 Unique）
```

**两个集合的分工**：

```
seq        = 会话的全局水位（这条会话一共产生到第几号）
seq_user   = 用户在这个会话里的可见范围（我能看到 [minSeq, maxSeq]，已读到 readSeq）
```

12 篇讲过 `seq`，14 篇讲过 `seq_user`。这里只强调**索引问题**：

> ⚠️ **两个缺失的唯一索引是同一个 bug 类型**：
> - `seq_conversation.go:16-20`：`conversation_id` 只有普通索引
> - `seq_user.go:16-20`：`(user_id, conversation_id)` 只有普通索引
>
> 后果：并发首次 upsert 时，MongoDB 的 `upsert` 不是原子的（它是「查 → 没有 → 插入」），可能插两条。之后 `$inc` 落在不同文档上，**seq 就重复了**。
>
> 正确做法是 `SetUnique(true)`。加上之后，第二条插入报 E11000，`$inc` 走 `upsert` 的 update 分支即可。这是一个「加一行代码就能修」的 bug，很适合在面试里当案例讲。

## 2.8 版本日志 `version_log`：增量同步的核心

```go
// model/version_log.go
type VersionLogTable struct {
    ID         primitive.ObjectID
    DID        string             // 文档 ID（如 group_id）
    Logs       []VersionLogElem   // 版本日志数组
    Version    uint               // 全局版本号
    Deleted    uint               // 删除水位
    LastUpdate time.Time
}

type VersionLogElem struct {
    EID        string    // 元素 ID（如 user_id）
    State      int32     // 1 插入 / 2 删除 / 3 更新
    Version    uint      // 本条版本号
    LastUpdate time.Time
}
```

索引（`mgo/version_log.go:33`）：`d_id` 唯一。

**这是一个「变更日志（change log）」模式**：

```
群组成员变化：
  t1: user1 入群 → Logs = [{EID: user1, State: INSERT, Version: 1}], Version = 1
  t2: user2 入群 → Logs = [..., {EID: user2, State: INSERT, Version: 2}], Version = 2
  t3: user1 退群 → Logs = [..., {EID: user1, State: DELETE, Version: 3}], Version = 3
```

客户端拿着 `lastVersion=1` 来同步，服务端返回 `version > 1` 的所有变更（2 和 3）。**不用全量拉成员列表**。

> 🔴 **为什么不用时间戳同步？** 因为**删除没有时间戳可查**。你删了 user1，从哪查「user1 是什么时候被我删的」？记录已经没了。版本日志把「删除」也变成一条可查的记录（`State: DELETE`），这是增量同步能工作的前提。17 篇讲过这个逻辑。

> 🟡 **为什么 Logs 是数组而不是一个集合？** 因为一个 `DID` 的变更通常不多（群成员变化），放一个文档里能**一次读回全部变更**。但要注意 16MB 上限——如果群成员频繁变动，`Logs` 会无限增长。`Deleted` 字段就是为了解决这个：定期把已确认的旧日志删掉（📌 待验证：具体的清理策略，我在 `mgo/version_log.go` 里没找到定时清理的调用方）。

`version_log` 一共有 **4 个集合**在用：`friend_version`、`group_member_version`、`group_join_version`、`conversation_version`。命名规律是 `<业务>_version`。

## 2.9 其余集合速览

| 集合 | 用途 | 关键点 |
|---|---|---|
| `group_request` | 入群申请 | 和 friend_request 同构，有 `handle_result` 状态机 |
| `log` | 客户端日志上报 | 索引 `(user_id, create_time)`，用于运营排查；20 篇讲过批量上报的性能问题 |
| `s3` | 对象存储元数据 | 三个索引：`name` 唯一、`create_time`、`key`；**`key` 非唯一**，因为秒传会让多个 name 指向同一 key（引用计数的前提） |
| `cache` | 图片缩略图等派生数据 | 索引看 `mgo/cache.go:20` |
| `client_config` | 客户端配置 | 索引 `(user_id, platform_id)` |
| `stream_msg` | 流式消息 | 📌 我只看到集合名定义，没有找到读写逻辑，推测是未启用的实验功能，**待验证** |

> 📌 **`stream_msg` 的定位**：`StreamMsgName = "stream_msg"` 在 `name.go` 里定义了，但 `grep -rn "StreamMsgName"` 只有定义处一个结果。这是一个**声明了但没用**的集合名。可能是为大模型流式输出预留的，也可能只是历史残留。**不要当成结论写进简历**。

## 2.10 索引全景表

把所有索引汇总，这是面试时能直接画出来的表：

| 集合 | 索引 | 唯一 | 服务哪种查询 |
|---|---|---|---|
| `user` | `user_id` | ✅ | 按 ID 查用户 |
| `friend` | `(owner_user_id, friend_user_id)` | ✅ | 好友列表 / 是否好友 |
| `friend_request` | `(from_user_id, to_user_id)`、`to_user_id` | ❌ | 发出的申请 / 收到的申请 |
| `black` | `(owner_user_id, block_user_id)` | ✅ | 黑名单列表 / 是否拉黑 |
| `group` | `group_id` | ✅ | 按 ID 查群 |
| `group_member` | `(group_id, user_id)` | ✅ | 群成员列表 / 是否在群 |
| `group_request` | 见 `mgo/group_request.go:22` | ❌ | 入群申请 |
| `conversation` | `(owner_user_id, conversation_id)`、`user_id` | ✅ / ❌ | 我的会话 / 我参与的会话 |
| `msg` | `doc_id` | ✅ | 按桶查消息 |
| `seq` | `conversation_id` | ❌ ⚠️ | 按会话取号（**缺唯一索引**） |
| `seq_user` | `(user_id, conversation_id)` | ❌ ⚠️ | 用户水位（**缺唯一索引**） |
| `s3` | `name`、`create_time`、`key` | ✅/❌/❌ | 对象元数据 |
| `log` | 见 `mgo/log.go:18` | ❌ | 日志查询 |
| `version_log` × 4 | `d_id` | ✅ | 增量同步 |

**规律**：**所有「一对一」的实体都建了唯一索引，所有「一对多」的查询键都建了普通索引**。这张表能直接支撑「你们项目怎么设计索引」这个面试题。

> ⚠️ 「缺唯一索引」的两处要靠 `upsert` 的 `$setOnInsert` 保证不重复，但 `$setOnInsert` 本身也不保证并发安全——两个请求同时 upsert 时，MongoDB 会尝试插入两条，唯一索引会让第二条失败。**没有唯一索引，就没有任何保护**。

## 2.11 连接的三种构建方式

`pkg/dbbuild/` 目录只有三个文件，但回答了「standalone 和微服务怎么共享连接」：

```go
// builder.go
type Builder interface {
    Mongo(ctx context.Context) (*mongoutil.Client, error)
    Redis(ctx context.Context) (redis.UniversalClient, error)
}

func NewBuilder(mongo *config.Mongo, redis *config.Redis) Builder {
    if config.Standalone() {
        return newStandalone(mongo, redis)
    }
    return &microservices{mongo: mongo, redis: redis}
}
```

**standalone 模式**（`standalone.go`）：

```go
var globalStandalone = &standalone{}   // 全局单例

func (x *standalone) Mongo(ctx context.Context) (*mongoutil.Client, error) {
    x.lock.Lock()
    defer x.lock.Unlock()
    if x.conn == nil {
        x.conn = make(map[string]any)
    }
    v, ok := x.conn[standaloneMongo]
    if !ok {
        var val standaloneConn[*mongoutil.Client]
        val.Conn, val.Err = mongoutil.NewMongoDB(ctx, x.mongo.Build())
        v = &val
        x.conn[standaloneMongo] = v
    }
    return v.(*standaloneConn[*mongoutil.Client]).result()
}
```

**这是「进程内连接复用」**：standalone 模式下，12 个服务跑在同一个进程里，如果每个服务都 `NewMongoDB` 一次，就会有 12 个连接池 × 100 = 1200 个连接。而单机 Mongo 默认 `maxIncomingConnections` 只有 65536，很容易打满。

所以用一个全局 map 缓存连接，**第一次调用时创建，之后复用**。注意这里还缓存了 `Err`——如果第一次创建失败，后续调用直接返回同一个错误，不会反复重试。

> 🟡 **这个设计的一个隐患**：`val.Err` 被永久缓存。如果启动时 Mongo 恰好不可用（比如容器还没起来），这个错误会一直返回，直到进程重启。没有重连机制。**在 K8s 里靠 liveness probe 重启进程解决**，在 standalone 里就是启动失败。

**微服务模式**（`microservices.go`）：

```go
func (x *microservices) Mongo(ctx context.Context) (*mongoutil.Client, error) {
    return mongoutil.NewMongoDB(ctx, x.mongo.Build())
}
```

每次调用新建连接。因为微服务模式下每个服务是**独立进程**，各自一个连接池是正确的。

## 2.12 连接参数的意义

`config/mongodb.yml` 里两个值值得单独说：

```yaml
maxPoolSize: 100
maxRetry: 10
```

**`maxPoolSize: 100`**：每个进程最多 100 个到 Mongo 的连接。

MongoDB 的连接是**线程模型**（每个连接一个 goroutine），不是事件循环。连接数打满时，新请求会在池里排队。100 这个值的意义：

```
单个进程 QPS ≈ 连接数 / 平均查询耗时
             = 100 / 0.001s
             = 100,000 QPS（理论上限）
```

实际远达不到（受 CPU、网络、Mongo 端并发限制）。但**如果查询慢（比如 50ms），100 个连接只能撑 2000 QPS**。所以连接数不是越大越好，先优化查询。

**`maxRetry: 10`**：连接失败重试次数。`mongoutil.NewMongoDB` 里有这段：

```go
for range config.MaxRetry {
    cli, err := connectMongo(ctx, opts)
    if err != nil && shouldRetry(ctx, err) {
        continue
    }
    ...
}
```

注意 `shouldRetry` —— **不是所有错误都重试**。网络错误可以重试，认证失败重试 10 次也没用。这个判断在 `common.go:48`。

## 2.13 批量操作：文档数据库的正确用法

最后讲一个**性能上的关键模式**。在关系库里，批量插入是：

```sql
INSERT INTO t VALUES (...), (...), (...);   -- 一条语句
```

MongoDB 里对应的是 `InsertMany`：

```go
// 典型的用法（mongoutil 封装）
mongoutil.InsertMany(ctx, coll, docs)
```

**为什么强调这个？** 因为最大的性能杀手是「循环单条插入」：

```go
// ❌ 错误写法
for _, doc := range docs {
    coll.InsertOne(ctx, doc)   // 100 次网络往返
}

// ✅ 正确写法
coll.InsertMany(ctx, docs)     // 1 次网络往返
```

100 次往返 × 0.5ms = 50ms，1 次往返 = 0.5ms，**差 100 倍**。

OpenIM 在消息落库上做得更极致：`batcher` 先把 500 条消息聚合成批（11 篇），然后同会话的消息合成一次 `FindOneAndUpdate`（带 `$push` 多个元素），**一次写入 100 条消息**（13 篇）。

> 🔴 **面试能用的结论**：**文档数据库的性能优化，80% 在「减少往返次数」上**。聚合写、聚合读、批量删，这三个模式掌握了，MongoDB 的性能问题就解决大半。

## 2.14 本篇小结

| 问题 | 答案 | 位置 |
|---|---|---|
| 为什么用 MongoDB | 高频更新同一行 + 无限追加日志 + 变长 payload，关系库不适合 | 1.1 |
| 单文档原子性带来什么 | 「已读」只能设计成水位，不能设计成每标记 | 1.2 |
| 为什么索引是 B 树不是 B+ 树 | 点查优先 vs 范围查询优先 | 1.3 |
| 复合索引为什么最左前缀 | 索引先按第一列排序，跳过后无序 | 1.3 |
| 唯一索引的真正作用 | 防并发插入，实现幂等 | 1.3 |
| 为什么几乎不用事务 | 幂等 > 事务；跨系统写事务管不了 | 1.4 |
| 事务怎么在单机上降级 | 探测 `isMaster` 有无 `setName`，无则直接执行 fn | 1.4 |
| 为什么不分片 | 应用层已分片（消息分桶 + 会话按 owner 分布） | 1.5 |
| 好友为什么存两份 | 备注/置顶是方向性字段 | 2.3 |
| 群成员为什么独立集合 | 16MB / 无法反查 / 并发冲突 | 2.5 |
| 版本日志解决什么 | 删除也变成可查记录，支持增量同步 | 2.8 |
| standalone 怎么复用连接 | 全局 map 缓存，避免 12 × 100 个连接 | 2.11 |

---

# 第三部分　实验

### 🧪 实验 21-1：看真实的数据长什么样

```bash
# 进容器
docker exec -it mongo mongosh -u im_app -p <密码> --authenticationDatabase im_v3 im_v3

# 集合列表与文档数
db.getCollectionNames().forEach(c => print(c, db[c].countDocuments()))

# 看一个会话文档
db.conversation.findOne({owner_user_id: "imAdmin"})

# 看索引
db.conversation.getIndexes()
db.seq.getIndexes()      # ← 验证 conversation_id 没有 unique
db.seq_user.getIndexes() # ← 验证 (user_id, conversation_id) 没有 unique
```

**预期**：`seq.getIndexes()` 返回的 `conversation_id_1` 里**没有** `"unique": true`。这就是 2.7 节说的 bug。

### 🧪 实验 21-2：验证最左前缀

```javascript
// 建测试数据
db.t.insertMany([
  {a: 1, b: 1}, {a: 1, b: 2}, {a: 2, b: 1}, {a: 2, b: 2}
])
db.t.createIndex({a: 1, b: 1})

// 能用索引
db.t.find({a: 1}).explain("executionStats")
// 看 winningPlan.stage 是否为 IXSCAN

// 用不上（全表扫）
db.t.find({b: 1}).explain("executionStats")
// 看 winningPlan.stage 是否为 COLLSCAN
```

**这是面试时最容易被追问的点**，自己跑一遍比背十遍强。

### 🧪 实验 21-3：验证事务降级

```bash
# 单机模式：没有 setName
docker exec -it mongo mongosh -u im_app -p <密码> --authenticationDatabase im_v3 \
  --eval 'db.adminCommand({isMaster:1}).setName'
# 输出 undefined

# 如果把 mongoMode 改成 replicaSet 并起三节点，会输出 rs0
```

---

# 第四部分　自测题

1. **为什么 IM 不适合用 MySQL 存消息？** 说出三个理由，每个理由配一个具体数字。
2. **MongoDB 的单文档原子性，如何影响了「已读」的数据模型？** 如果 MongoDB 支持跨文档事务，你会怎么设计已读？
3. **复合索引 `(owner_user_id, conversation_id)` 能不能支持「按 conversation_id 查询」？** 为什么？
4. **唯一索引除了「防重复」，还有什么作用？** 举一个 OpenIM 里的例子。
5. **`mongoutil` 的事务在多文档场景下怎么在单机上不报错？** 这个设计的代价是什么？
6. **OpenIM 为什么不做 MongoDB 分片？** 它在应用层做了什么等价的事？
7. **`seq` 和 `seq_user` 两个集合分别存什么？** 它们的索引缺了什么，会导致什么后果？
8. **`version_log` 的 `Logs` 数组会不会无限增长？** `Deleted` 字段是干什么的？
9. **standalone 模式为什么要缓存连接？** 如果不缓存会怎样？
10. **批量写入为什么比循环单条写入快 100 倍？** 差的是什么？

---

# 第五部分　面试话术

**问：你们项目的数据模型是怎么设计的？**

> 「核心是三个判断。
> 第一，**按访问模式选存储**。消息是只追加、按会话聚合、payload 变长的数据，关系库会有写放大和行数上限问题，所以选文档数据库。
> 第二，**用单文档原子性倒推业务设计**。MongoDB 只保证单文档原子，所以『已读』不能设计成每人一个标记（那是跨文档），只能设计成每人一个水位 maxSeq，未读数用 `maxSeq - readSeq` 减法算出来。这是被存储能力逼出来的设计，不是拍脑袋。
> 第三，**索引跟着查询走，不是跟着字段走**。我们有 18 个集合，每个集合的索引都对应一条确定的查询路径。比如会话集合建了 `(owner_user_id, conversation_id)` 唯一索引服务『我的会话列表』，又建了 `user_id` 单列索引服务『这个人参与的所有会话』——因为群解散时要清所有人的会话。」

**追问：你们用事务吗？**

> 「几乎不用，全项目只有 5 处。因为消息链路上有三类写操作：写 Redis 的 seq、写 Mongo 的消息、发 Kafka 的推送，这是三个不同系统，MongoDB 事务管不了，上分布式事务的复杂度远超收益。
> 我们的策略是**用幂等代替事务**：消息写入用 `doc_id` 唯一索引 + 『先 update 后 insert』实现幂等，Kafka 消费失败就不提交位点重投。只要每个环节都幂等，最终一致就够了。
> 唯一用事务的地方是群解散和会话批量更新这种多文档写。而且这里有个我觉得挺巧妙的设计：`mongoutil` 启动时会跑 `isMaster` 探测有没有 `setName` 字段，判断是不是副本集。不是副本集就把事务函数置空，`Transaction` 直接执行回调——同一份代码在单机开发和副本集生产都能跑，没有任何环境分支。」

**追问：18 个集合，你觉得哪个设计得最好？哪个有问题？**

> 「最好的我觉得是 `version_log`。它把『删除』也变成一条可查的记录（`State: DELETE`），这是增量同步能工作的前提——如果只存最终状态，用户退群之后你根本查不到『他什么时候退的』，客户端就没法做增量拉取。
> 有问题的我找到两个：`seq` 集合的 `conversation_id` 索引和 `seq_user` 的 `(user_id, conversation_id)` 索引都**没有设唯一**。这两个都是并发首次 upsert 时可能插两条文档的地方，一旦插了两条，`$inc` 会分别作用在不同文档上，seq 就重复了。加一行 `SetUnique(true)` 就能修，但影响是消息序号重复，属于严重 bug。」

---

> **下一篇**：22 Redis 使用与缓存策略。会讲清 Redis 为什么单线程还这么快、五种数据结构在 OpenIM 里各自承担什么、以及 rockscache 这个「HASH 三字段 + 延迟删除」的一致性方案。
