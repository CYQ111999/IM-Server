# Kafka 说明

本文档说明本项目如何使用 Kafka，以及通过 Docker Compose 快速安装 Kafka 的方法。

## 安装 Kafka

通过仓库根目录的 `docker-compose.yml` 即可启动 Kafka：

```bash
docker compose up -d
```

执行后 Kafka 随中间件一起启动。可用以下命令确认容器状态：

```bash
docker ps | grep kafka
```

输出示例：

```
be416b5a0851   bitnamilegacy/kafka:3.5.1 "/opt/bitnami/script…"   3 days ago   Up 2 days   9092/tcp, 0.0.0.0:19094->9094/tcp, :::19094->9094/tcp   kafka
```

## 本项目用到的 Topic

消息链路依赖三个 topic（由 `pkg/common/storage/kafka` 与 `pkg/mqbuild` 使用）：

- `toRedis`：在线消息投递到 Redis，供 WebSocket 网关推送；
- `toMongo`：消息落库到 MongoDB；
- `toPush`：推送到 push 服务。

Topic 在 `config/kafka.yml` 中配置，Kafka 开启自动创建时无需手动建 topic。

## 常用命令

### 进入 Kafka 容器

```bash
docker exec -it kafka bash
```

### 容器内命令工具

- `kafka-topics.sh`：创建、删除、列出、修改 topic；
- `kafka-console-producer.sh`：从命令行向指定 topic 发送消息；
- `kafka-console-consumer.sh`：从命令行读取消息，可指定 topic；
- `kafka-consumer-groups.sh`：管理消费组信息。

### 列出 Topic

```bash
kafka-topics.sh --list --bootstrap-server localhost:9092
```

### 创建 Topic

```bash
kafka-topics.sh --create --bootstrap-server localhost:9092 --replication-factor 1 --partitions 8 --topic your_topic_name
```

### 生产消息

```bash
kafka-console-producer.sh --broker-list localhost:9092 --topic your_topic_name
```

### 消费消息

```bash
kafka-console-consumer.sh --bootstrap-server localhost:9092 --topic your_topic_name --from-beginning
```

> `--from-beginning` 表示从 topic 起点开始消费；省略时只消费新消息。
